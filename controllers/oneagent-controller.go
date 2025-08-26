package controllers

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/runtime"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// PersistentVolumeClaimReconciler ensures PVCs exist for Pods annotated by the webhook
type PersistentVolumeClaimReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// Reconcile ensures annotated Pods always have a PVC
func (r *PersistentVolumeClaimReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var pod corev1.Pod
	if err := r.Get(ctx, req.NamespacedName, &pod); err != nil {
		if errors.IsNotFound(err) {
			// Pod deleted → PVC cleanup is automatic via OwnerReferences
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

	// Check if PVC already exists
	var pvc corev1.PersistentVolumeClaim
	err := r.Get(ctx, client.ObjectKey{Namespace: pod.Namespace, Name: claimName}, &pvc)
	if err == nil {
		switch pvc.Status.Phase {
		case corev1.ClaimBound:
			logger.Info("PVC already bound", "pvc", claimName, "pod", pod.Name)
			return ctrl.Result{}, nil
		case corev1.ClaimPending:
			logger.Info("PVC exists but not yet bound", "pvc", claimName, "pod", pod.Name)
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		default:
			logger.Info("PVC in unexpected phase", "pvc", claimName, "phase", pvc.Status.Phase)
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
	}
	if !errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	// PVC does not exist → create it
	qty, parseErr := resource.ParseQuantity(storageSize)
	if parseErr != nil {
		logger.Error(parseErr, "Invalid storage-size annotation, defaulting to 2Gi")
		qty = resource.MustParse("2Gi")
	}

	pvc = corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      claimName,
			Namespace: pod.Namespace,
			Labels: map[string]string{
				"created-by": "pvc-webhook",
				"pod":        pod.Name,
			},
			// Garbage collector will delete PVC when Pod is deleted
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
					corev1.ResourceStorage: qty,
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

	// Emit event via client-go EventRecorder
	if r.Recorder != nil {
		r.Recorder.Eventf(&pod, corev1.EventTypeNormal, "PVCProvisioned",
			"Created PVC %s for Pod %s", claimName, pod.Name)
	}

	// Requeue to check binding status
	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// SetupWithManager registers this reconciler with the controller-runtime manager
func (r *PersistentVolumeClaimReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.Recorder = mgr.GetEventRecorderFor("pvc-webhook")
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Pod{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Complete(r)
}

