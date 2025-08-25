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

type PVCReconciler struct {
    client.Client
    DefaultSize  string
    DefaultClass string
}

func (r *PVCReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    logger := log.FromContext(ctx)

    var pod corev1.Pod
    if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
        if errors.IsNotFound(err) {
            return ctrl.Result{}, nil
        }
        return ctrl.Result{}, err
    }

    claimName, ok := pod.Annotations["pvc-webhook/claim"]
    if !ok || claimName == "" {
        return ctrl.Result{}, nil // nothing to do
    }

    // ✅ Handle pod deletion (explicit PVC cleanup)
    if !pod.ObjectMeta.DeletionTimestamp.IsZero() {
        var pvc corev1.PersistentVolumeClaim
        err := r.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: claimName}, &pvc)
        if err == nil {
            if delErr := r.Delete(ctx, &pvc); delErr != nil {
                logger.Error(delErr, "failed to delete PVC", "pvc", claimName)
                return ctrl.Result{}, delErr
            }
            logger.Info("Deleted PVC for Pod", "pvc", claimName, "pod", pod.Name)
        }
        return ctrl.Result{}, nil
    }

    // ✅ Normal PVC creation path
    sizeStr := pod.Annotations["pvc-webhook/storage-size"]
    if sizeStr == "" {
        sizeStr = r.DefaultSize
    }
    size, err := resource.ParseQuantity(sizeStr)
    if err != nil {
        logger.Error(err, "invalid storage size", "value", sizeStr)
        return ctrl.Result{}, nil
    }

    className := pod.Annotations["pvc-webhook/storage-class"]
    if className == "" {
        className = r.DefaultClass
    }

    var pvc corev1.PersistentVolumeClaim
    err = r.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: claimName}, &pvc)
    if err == nil {
	logger.Info("PVC already exists for Pod", "pvc", claimName, "pod", pod.Name)
	return ctrl.Result{}, nil
    }
    if !errors.IsNotFound(err) {
        return ctrl.Result{}, err
    }

    pvc = corev1.PersistentVolumeClaim{
        ObjectMeta: metav1.ObjectMeta{
            Name:      claimName,
            Namespace: pod.Namespace,
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
                    corev1.ResourceStorage: size,
                },
            },
        },
    }

    sc := className
    pvc.Spec.StorageClassName = &sc

    if err := r.Create(ctx, &pvc); err != nil {
        return ctrl.Result{}, err
    }

    logger.Info("Created PVC for Pod", "pvc", claimName, "pod", pod.Name)
    return ctrl.Result{}, nil
}

