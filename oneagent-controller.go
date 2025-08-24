package controllers

import (
    "context"
    "fmt"

    corev1 "k8s.io/api/core/v1"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/api/errors"
    "k8s.io/apimachinery/pkg/api/resource"
    ctrl "sigs.k8s.io/controller-runtime"
    "sigs.k8s.io/controller-runtime/pkg/client"
    "sigs.k8s.io/controller-runtime/pkg/log"
)

// PodReconciler reconciles Pods that require PVCs
type PodReconciler struct {
    client.Client
}

// RBAC: allow managing Pods and PVCs
// +kubebuilder:rbac:groups="",resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;create;update;watch

func (r *PodReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    logger := log.FromContext(ctx)

    // Fetch the Pod
    var pod corev1.Pod
    if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
        return ctrl.Result{}, client.IgnoreNotFound(err)
    }

    // Check if pod has been annotated by the webhook
    pvcName := pod.Annotations["pvc-webhook/claim"]
    if pvcName == "" {
        return ctrl.Result{}, nil // nothing to do
    }

    sizeStr := pod.Annotations["pvc-webhook/storage-size"]
    if sizeStr == "" {
        sizeStr = "2Gi" // default fallback
    }
    size, err := resource.ParseQuantity(sizeStr)
    if err != nil {
        logger.Error(err, "invalid storage size", "value", sizeStr)
        return ctrl.Result{}, nil
    }

    className := pod.Annotations["pvc-webhook/storage-class"]
    if className == "" {
       className = "robin-repl-3" // default fallback storage class
    }

    // Check if PVC already exists
    var pvc corev1.PersistentVolumeClaim
    err = r.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: pvcName}, &pvc)
    if err == nil {
        // PVC already exists → nothing to do
        return ctrl.Result{}, nil
    }
    if !errors.IsNotFound(err) {
        return ctrl.Result{}, err
    }

    // PVC does not exist → create it
    pvc = corev1.PersistentVolumeClaim{
        ObjectMeta: metav1.ObjectMeta{
            Name:      pvcName,
            Namespace: pod.Namespace,
            OwnerReferences: []metav1.OwnerReference{
                *metav1.NewControllerRef(&pod, corev1.SchemeGroupVersion.WithKind("Pod")),
            },
        },
        Spec: corev1.PersistentVolumeClaimSpec{
            AccessModes: []corev1.PersistentVolumeAccessMode{
                corev1.ReadWriteOnce,
            },
            /* Resources: corev1.ResourceRequirements{
                Requests: corev1.ResourceList{
                    corev1.ResourceStorage: size,
                },
            }, */
	    Resources: corev1.VolumeResourceRequirements{
                Requests: corev1.ResourceList{
                    corev1.ResourceStorage: size,
                },
            },
        },
    }
    if className != "" {
        pvc.Spec.StorageClassName = &className
    }

    if err := r.Create(ctx, &pvc); err != nil {
        logger.Error(err, "failed to create PVC", "pvc", pvcName)
        return ctrl.Result{}, err
    }

    logger.Info("created PVC for Pod", "pvc", pvcName, "pod", fmt.Sprintf("%s/%s", pod.Namespace, pod.Name))
    return ctrl.Result{}, nil
}

// SetupWithManager wires the controller to manager
func (r *PodReconciler) SetupWithManager(mgr ctrl.Manager) error {
    return ctrl.NewControllerManagedBy(mgr).
        For(&corev1.Pod{}).
        Complete(r)
}

