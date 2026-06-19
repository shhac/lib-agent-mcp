package agentmcp

import "testing"

func TestTranslateSuccessRecordsAndMeta(t *testing.T) {
	r := runResult{stdout: []byte(
		`{"id":"w-1"}` + "\n" +
			`{"id":"w-2"}` + "\n" +
			`{"@pagination":{"has_more":true}}` + "\n")}

	res := translate(r)
	if res["isError"].(bool) {
		t.Fatal("unexpected isError")
	}
	sc := res["structuredContent"].(map[string]any)
	if got := len(sc["records"].([]any)); got != 2 {
		t.Errorf("records = %d, want 2", got)
	}
	meta, ok := sc["meta"].(map[string]any)
	if !ok {
		t.Fatalf("missing meta: %v", sc)
	}
	if _, ok := meta["@pagination"]; !ok {
		t.Error("@pagination not captured as metadata")
	}
	if len(res["content"].([]any)) == 0 {
		t.Error("expected a text content fallback")
	}
}

func TestTranslateErrorSurfacesFixableBy(t *testing.T) {
	r := runResult{
		stderr:   []byte(`{"error":"widget \"x\" not found","fixable_by":"agent","hint":"list them"}` + "\n"),
		exitCode: 1,
	}
	res := translate(r)
	if !res["isError"].(bool) {
		t.Fatal("expected isError for non-zero exit")
	}
	sc := res["structuredContent"].(map[string]any)
	errObj, ok := sc["error"].(map[string]any)
	if !ok {
		t.Fatalf("missing structured error: %v", sc)
	}
	if errObj["fixable_by"] != "agent" {
		t.Errorf("fixable_by = %v, want agent", errObj["fixable_by"])
	}
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	if text == "" {
		t.Error("error text content should be non-empty")
	}
}

func TestTranslateNonJSONStdoutDegradesToText(t *testing.T) {
	r := runResult{stdout: []byte("not json at all\nstill not json\n")}
	res := translate(r)
	sc := res["structuredContent"].(map[string]any)
	if got := len(sc["records"].([]any)); got != 0 {
		t.Errorf("records = %d, want 0 for non-JSON output", got)
	}
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	if text == "" {
		t.Error("raw stdout should survive as text content")
	}
}
