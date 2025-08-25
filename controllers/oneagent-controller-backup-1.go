package controllers

import (
    "context"

    corev1 "k8s.io/api/core/v1"
    "k8s.io/apimachinery/pkg/api/errors"
    "k8s.io/apimachinery/pkg/api/resource"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/log"
)

// PersistentVolumeClaimReconciler ensures PVCs exist for Pods annotated by the webhook
type PersistentVolumeClaimReconciler struct {
    client.Client
}

// Reconcile runs when Pods are created/updated/deleted
func (r *PersistentVolumeClaimReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    logger := log.FromContext(ctx)

    var pod corev1.Pod
    if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
        if errors.IsNotFound(err) {
            // Pod deleted → also delete PVC if one was linked
            // Construct PVC name from claim annotation (same namespace)
            // Note: if Pod object already gone, we can’t read annotations directly
            // Instead, you might rely on deterministic naming convention
            return ctrl.Result{}, nil
        }
        return ctrl.Result{}, err
    }

    // Skip Pods without PVC annotation
    claimName := pod.Annotations["pvc-webhook/claim"]
    if claimName == "" {
        return ctrl.Result{}, nil
    }

    storageSize := pod.Annotations["pvc-webhook/storage-size"]
    if storageSize == "" {
        storageSize = "2Gi"
    }

    storageClass := pod.Annotations["pvc-webhook/storage-class"]

    // Check if PVC exists
    var pvc corev1.PersistentVolumeClaim
    err := r.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: claimName}, &pvc)
    if err == nil {
        logger.Info("PVC already exists", "pvc", claimName, "pod", pod.Name)
        return ctrl.Result{}, nil
    }
    if !errors.IsNotFound(err) {
        return ctrl.Result{}, err
    }

    // PVC does not exist → create it
    pvc = corev1.PersistentVolumeClaim{
        ObjectMeta: metav1.ObjectMeta{
            Name:      claimName,
            Namespace: pod.Namespace,
            Labels: map[string]string{
                "created-by": "pvc-webhook",
                "pod":        pod.Name,
            },
            OwnerReferences: []metav1.OwnerReference{
                *metav1.NewControllerRef(&pod, corev1.SchemeGroupVersion.WithKind("Pod")),
            },
        },
        Spec: corev1.PersistentVolumeClaimSpec{
            AccessModes: []corev1.PersistentVolumeAccessMode{
                corev1.ReadWriteOnce,
            },
            Resources: corev1.VolumeResourceRequirements{
                Requests: corev1.ResourceList{
                    corev1.ResourceStorage: resource.MustParse(storageSize),
                },
            },
        },
    }

    if storageClass != "" {
        pvc.Spec.StorageClassName = &storageClass
    }

    if err := r.Create(ctx, &pvc); err != nil {
        return ctrl.Result{}, err
    }

    logger.Info("Created PVC for Pod", "pvc", claimName, "pod", pod.Name)
    return ctrl.Result{}, nil
}

// SetupWithManager registers this reconciler with the controller-runtime manager
func (r *PersistentVolumeClaimReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&corev1.Pod{}).
        Owns(&corev1.PersistentVolumeClaim{}).
        Complete(r)
}

