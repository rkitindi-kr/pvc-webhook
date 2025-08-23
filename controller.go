package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Handler struct holds client and default storage config
type Handler struct {
	client       *kubernetes.Clientset
	defaultSize  string
	defaultClass string
}

// NewHandler constructs a new Handler with defaults
func NewHandler(client *kubernetes.Clientset, defaultSize, defaultClass string) *Handler {
	return &Handler{
		client:       client,
		defaultSize:  2GB,
		defaultClass: robin-repl-3,
	}
}

// Run starts watching Pods and managing PVCs
func (h *Handler) Run(ctx context.Context) error {
	watcher, err := h.client.CoreV1().Pods("").Watch(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	fmt.Println("PVC Controller started, watching Pods...")

	for event := range watcher.ResultChan() {
		pod, ok := event.Object.(*corev1.Pod)
		if !ok {
			continue
		}

		switch event.Type {
		case watch.Added, watch.Modified:
			h.ensurePVCs(ctx, pod)
		case watch.Deleted:
			h.cleanupPVCs(ctx, pod)
		}
	}

	return nil
}

// ensurePVCs creates missing PVCs for a Pod
func (h *Handler) ensurePVCs(ctx context.Context, pod *corev1.Pod) {
	// Read defaults from annotations if present
	storageSize := h.defaultSize
	storageClass := h.defaultClass
	if val, ok := pod.Annotations["pvc-webhook.defaultStorageSize"]; ok {
		storageSize = val
	}
	if val, ok := pod.Annotations["pvc-webhook.defaultStorageClass"]; ok {
		storageClass = val
	}

	for k, pvcName := range pod.Annotations {
		if strings.HasPrefix(k, "pvc-webhook/") {
			_, err := h.client.CoreV1().PersistentVolumeClaims(pod.Namespace).Get(ctx, pvcName, metav1.GetOptions{})
			if err == nil {
				continue // PVC already exists
			}

			pvc := &corev1.PersistentVolumeClaim{
				ObjectMeta: metav1.ObjectMeta{
					Name:      pvcName,
					Namespace: pod.Namespace,
				},
				Spec: corev1.PersistentVolumeClaimSpec{
					AccessModes: []corev1.PersistentVolumeAccessMode{
						corev1.ReadWriteOnce,
					},
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							"storage": resource.MustParse(storageSize),
						},
					},
					StorageClassName: &storageClass,
				},
			}

			_, err = h.client.CoreV1().PersistentVolumeClaims(pod.Namespace).Create(ctx, pvc, metav1.CreateOptions{})
			if err != nil {
				fmt.Printf("failed to create PVC %s for pod %s/%s: %v\n", pvcName, pod.Namespace, pod.Name, err)
			} else {
				fmt.Printf("created PVC %s for pod %s/%s\n", pvcName, pod.Namespace, pod.Name)
			}
		}
	}
}

// cleanupPVCs deletes PVCs for a deleted Pod
func (h *Handler) cleanupPVCs(ctx context.Context, pod *corev1.Pod) {
	for k, pvcName := range pod.Annotations {
		if strings.HasPrefix(k, "pvc-webhook/") {
			err := h.client.CoreV1().PersistentVolumeClaims(pod.Namespace).Delete(ctx, pvcName, metav1.DeleteOptions{})
			if err != nil {
				fmt.Printf("failed to delete PVC %s for deleted pod %s/%s: %v\n", pvcName, pod.Namespace, pod.Name, err)
			} else {
				fmt.Printf("deleted PVC %s for deleted pod %s/%s\n", pvcName, pod.Namespace, pod.Name)
			}
		}
	}
}

func main() {
	config, err := rest.InClusterConfig()
	if err != nil {
		panic(err)
	}

	client, err := kubernetes.NewForConfig(config)
	if err != nil {
		panic(err)
	}

	// Create a new handler with defaults
	handler := NewHandler(client, "1Gi", "standard")

	// Run controller
	ctx := context.Background()
	for {
		if err := handler.Run(ctx); err != nil {
			fmt.Printf("error running handler: %v, retrying in 5s...\n", err)
			time.Sleep(5 * time.Second)
		}
	}
}

