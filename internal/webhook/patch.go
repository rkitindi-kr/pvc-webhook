package webhook

import "encoding/json"

type patchOp struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	Value json.RawMessage `json:"value,omitempty"`
}

func addOp(path string, v any) patchOp {
	b, _ := json.Marshal(v)
	return patchOp{Op: "add", Path: path, Value: b}
}
func replaceOp(path string, v any) patchOp {
	b, _ := json.Marshal(v)
	return patchOp{Op: "replace", Path: path, Value: b}
}

