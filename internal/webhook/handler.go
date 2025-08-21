package webhook

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"

	"github.com/go-logr/logr"
)

var (
	scheme  = runtime.NewScheme()
	codecs  = serializer.NewCodecFactory(scheme)
	deser   = codecs.UniversalDeserializer()
	nameRe  = regexp.MustCompile(`[^a-z0-9\-]`)
	maxName = 63
)

const (
	convertedAnno = "pvc-webhook/converted"
)

type Handler struct {
	log              logr.Logger
	defaultSize      string
	defaultSC        string
	defaultAccess    string
}

func init() {
	_ = corev1.AddToScheme(scheme)
	_ = admissionv1.AddToScheme(scheme)
}

func NewHandler(log logr.Logger) http.Handler {
	h := &Handler{
		log:           log,
		defaultSize:   getEnv("DEFAULT_SIZE", "10Gi"),
        defaultSC:     getEnv("DEFAULT_STORAGE_CLASS", "standard"),
        defaultAccess: getEnv("DEFAULT_ACCESS_MODES", "ReadWriteOnce"),
	}
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var review admissionv1.AdmissionReview
	if err := json.NewDecoder(r.Body).Decode(&review); err != nil {
		writeReview(w, toErrorResponse(review, err))
		return
	}

	if review.Request == nil || review.Request.Kind.Kind != "Pod" {
		writeReview(w, allow(review))
		return
	}

	// Decode current Pod
	pod := &corev1.Pod{}
	if _, _, err := deser.Decode(review.Request.Object.Raw, nil, pod); err != nil {
		writeReview(w, toErrorResponse(review, fmt.Errorf("decode pod: %w", err)))
		return
	}

	// Idempotency: skip if already converted
	if pod.Annotations != nil && pod.Annotations[convertedAnno] == "true" {
		writeReview(w, allow(review))
		return
	}

	ops := []patchOp{}
	addAnno := map[string]string{}
	converted := false

	for i, v := range pod.Spec.Volumes {
		if v.EmptyDir == nil {
			continue
		}
		converted = true

		// derive parameters
		volKey := fmt.Sprintf("pvc-webhook.vol/%s", v.Name)
		size := pick(pod.Annotations[volKey+".size"], h.defaultSize)
		sc := pick(pod.Annotations[volKey+".storageClass"], h.defaultSC)
		am := pick(pod.Annotations[volKey+".accessModes"], h.defaultAccess)

		claim := sanitize(fmt.Sprintf("pvc-%s-%s-%s", pod.Namespace, pod.Name, v.Name))
		if len(claim) > maxName { claim = claim[:maxName] }

		// replace volume
		newVol := corev1.Volume{
			Name: v.Name,
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: claim,
				},
			},
		}
		ops = append(ops, replaceOp(fmt.Sprintf("/spec/volumes/%d", i), newVol))

		// record parameters for the controller
		addAnno[fmt.Sprintf("%s.size", volKey)] = size
		addAnno[fmt.Sprintf("%s.storageClass", volKey)] = sc
		addAnno[fmt.Sprintf("%s.accessModes", volKey)] = am
		addAnno[fmt.Sprintf("%s.claimName", volKey)] = claim
	}

	if !converted {
		writeReview(w, allow(review))
		return
	}

	// ensure annotations map exists and add ours + converted flag
	if pod.Annotations == nil {
		ops = append(ops, addOp("/metadata/annotations", map[string]string{}))
	}
	for k, v := range addAnno {
		ops = append(ops, addOp(pathEscape("/metadata/annotations/"+k), v))
	}
	ops = append(ops, addOp(pathEscape("/metadata/annotations/"+convertedAnno), "true"))

	patchBytes, err := json.Marshal(ops)
	if err != nil {
		writeReview(w, toErrorResponse(review, fmt.Errorf("marshal patch: %w", err)))
		return
	}

	resp := &admissionv1.AdmissionResponse{
		UID:     review.Request.UID,
		Allowed: true,
		Patch:   patchBytes,
		PatchType: func() *admissionv1.PatchType {
			pt := admissionv1.PatchTypeJSONPatch
			return &pt
		}(),
		Result: &metav1.Status{Message: "converted emptyDir to PVC"},
	}
	writeReview(w, &admissionv1.AdmissionReview{Response: resp})
}

func writeReview(w http.ResponseWriter, ar *admissionv1.AdmissionReview) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ar)
}

func allow(in admissionv1.AdmissionReview) *admissionv1.AdmissionReview {
	return &admissionv1.AdmissionReview{
		Response: &admissionv1.AdmissionResponse{
			UID:     in.Request.UID,
			Allowed: true,
		},
	}
}

func toErrorResponse(in admissionv1.AdmissionReview, err error) *admissionv1.AdmissionReview {
	st := metav1.Status{Message: err.Error(), Code: http.StatusInternalServerError}
	return &admissionv1.AdmissionReview{Response: &admissionv1.AdmissionResponse{
		UID: in.Request.UID, Allowed: false, Result: &st,
	}}
}

func getEnv(k, def string) string { if v := os.Getenv(k); v != "" { return v }; return def }
func pick(vals ...string) string   { for _, v := range vals { if strings.TrimSpace(v) != "" { return v } }; return "" }

func sanitize(s string) string {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "_", "-")
	s = nameRe.ReplaceAllString(s, "-")
	for strings.Contains(s, "--") { s = strings.ReplaceAll(s, "--", "-") }
	return strings.Trim(s, "-")
}

func pathEscape(p string) string {
	// jsonpatch paths must escape "~" and "/" per RFC6901
	p = strings.ReplaceAll(p, "~", "~0")
	p = strings.ReplaceAll(p, "/", "~1")
	return "/" + strings.TrimPrefix(p, "/")
}

