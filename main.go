package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	admv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
)

var (
	scheme       = runtime.NewScheme()
	codecs       = serializer.NewCodecFactory(scheme)
	deserializer = codecs.UniversalDeserializer()
)

// 🔹 Helper function to escape JSON pointer keys
func escapeJSONPointer(s string) string {
	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")
	return s
}

// Handle incoming admission review requests
func mutatePods(w http.ResponseWriter, r *http.Request) {
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "unable to read body", http.StatusBadRequest)
		return
	}

	var admissionReview admv1.AdmissionReview
	if _, _, err := deserializer.Decode(body, nil, &admissionReview); err != nil {
		http.Error(w, "unable to decode body", http.StatusBadRequest)
		return
	}

	req := admissionReview.Request
	if req.Kind.Kind != "Pod" {
		writeResponse(w, admissionReview, nil)
		return
	}

	// Decode Pod
	var pod corev1.Pod
	if err := json.Unmarshal(req.Object.Raw, &pod); err != nil {
		http.Error(w, "could not unmarshal pod", http.StatusBadRequest)
		return
	}

	patches := []map[string]interface{}{}

	// Mutate each volume if it's emptyDir
	for i, vol := range pod.Spec.Volumes {
		if vol.EmptyDir != nil {
			// PVC name convention
			pvcName := fmt.Sprintf("pvc-%s-%s", pod.Name, vol.Name)

			// Replace emptyDir with PVC
			patch := map[string]interface{}{
				"op":    "replace",
				"path":  fmt.Sprintf("/spec/volumes/%d", i),
				"value": map[string]interface{}{
					"name": vol.Name,
					"persistentVolumeClaim": map[string]interface{}{
						"claimName": pvcName,
					},
				},
			}
			patches = append(patches, patch)

			// Add annotation so controller knows to create this PVC
			annPath := "/metadata/annotations"
			if pod.Annotations == nil || len(pod.Annotations) == 0 {
				// If no annotations exist, add the whole map
				patchAdd := map[string]interface{}{
					"op":   "add",
					"path": annPath,
					"value": map[string]string{
						"pvc-webhook/" + vol.Name: pvcName,
					},
				}
				patches = append(patches, patchAdd)
				pod.Annotations = map[string]string{} // prevent duplicate "add"
			} else {
				// Add a single key under existing annotations
				patchAnn := map[string]interface{}{
					"op":    "add",
					"path":  fmt.Sprintf("%s/%s", annPath, escapeJSONPointer("pvc-webhook/"+vol.Name)),
					"value": pvcName,
				}
				patches = append(patches, patchAnn)
			}
		}
	}

	patchBytes, _ := json.Marshal(patches)
	writeResponse(w, admissionReview, patchBytes)
}

// Write AdmissionReview response back to API server
func writeResponse(w http.ResponseWriter, ar admv1.AdmissionReview, patch []byte) {
	review := admv1.AdmissionReview{
		TypeMeta: ar.TypeMeta,
		Response: &admv1.AdmissionResponse{
			UID:     ar.Request.UID,
			Allowed: true,
		},
	}
	if patch != nil {
		pt := admv1.PatchTypeJSONPatch
		review.Response.PatchType = &pt
		review.Response.Patch = patch
	}

	resp, _ := json.Marshal(review)
	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}

func main() {
	http.HandleFunc("/mutate", mutatePods)
	// TLS certs should be mounted at /tls/tls.crt and /tls/tls.key
	fmt.Println("Starting webhook server on :8443 ...")
	if err := http.ListenAndServeTLS(":8443", "/tls/tls.crt", "/tls/tls.key", nil); err != nil {
		panic(err)
	}
}

