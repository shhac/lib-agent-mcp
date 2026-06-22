package agentmcp

import "testing"

func TestTranslateSuccessRecordsAndMeta(t *testing.T) {
	r := runResult{stdout: []byte(
		`{"id":"w-1"}` + "\n" +
			`{"id":"w-2"}` + "\n" +
			`{"@pagination":{"has_more":true}}` + "\n")}

	res := translate(r)
	if res.IsError {
		t.Fatal("unexpected isError")
	}
	sc := res.StructuredContent
	if sc == nil {
		t.Fatal("missing structuredContent")
	}
	if got := len(sc.Records); got != 2 {
		t.Errorf("records = %d, want 2", got)
	}
	if sc.Meta == nil {
		t.Fatalf("missing meta: %+v", sc)
	}
	if _, ok := sc.Meta["@pagination"]; !ok {
		t.Error("@pagination not captured as metadata")
	}
	if len(res.Content) == 0 {
		t.Error("expected a text content fallback")
	}
}

func TestTranslateErrorSurfacesFixableBy(t *testing.T) {
	r := runResult{
		stderr:   []byte(`{"error":"widget \"x\" not found","fixable_by":"agent","hint":"list them"}` + "\n"),
		exitCode: 1,
	}
	res := translate(r)
	if !res.IsError {
		t.Fatal("expected isError for non-zero exit")
	}
	errObj := res.StructuredContent.Error
	if errObj == nil {
		t.Fatalf("missing structured error: %+v", res.StructuredContent)
	}
	if errObj["fixable_by"] != "agent" {
		t.Errorf("fixable_by = %v, want agent", errObj["fixable_by"])
	}
	if res.Content[0].Text == "" {
		t.Error("error text content should be non-empty")
	}
}

func TestTranslateNonJSONStdoutDegradesToText(t *testing.T) {
	r := runResult{stdout: []byte("not json at all\nstill not json\n")}
	res := translate(r)
	if got := len(res.StructuredContent.Records); got != 0 {
		t.Errorf("records = %d, want 0 for non-JSON output", got)
	}
	if res.Content[0].Text == "" {
		t.Error("raw stdout should survive as text content")
	}
}

func TestTranslateCapturesMultipleMetaKeys(t *testing.T) {
	r := runResult{stdout: []byte(
		`{"id":"a"}` + "\n" +
			`{"@pagination":{"has_more":false}}` + "\n" +
			`{"@counts":{"n":1}}` + "\n")}
	res := translate(r)
	sc := res.StructuredContent
	if got := len(sc.Records); got != 1 {
		t.Errorf("records = %d, want 1", got)
	}
	for _, k := range []string{"@pagination", "@counts"} {
		if _, ok := sc.Meta[k]; !ok {
			t.Errorf("missing meta key %q", k)
		}
	}
}

func TestTranslateSuccessAppendsStderrNotice(t *testing.T) {
	r := runResult{
		stdout: []byte(`{"id":"a"}` + "\n"),
		stderr: []byte(`{"notice":"compact projection"}` + "\n"),
	}
	res := translate(r)
	if res.IsError {
		t.Fatal("notice on stderr must not make a success a failure")
	}
	if got := len(res.Content); got != 2 {
		t.Errorf("content blocks = %d, want 2 (records + notice)", got)
	}
}

func TestSingleMetaKey(t *testing.T) {
	if k, _, ok := singleMetaKey(map[string]any{"@x": 1}); !ok || k != "@x" {
		t.Error(`{"@x":1} should be metadata`)
	}
	if _, _, ok := singleMetaKey(map[string]any{"x": 1}); ok {
		t.Error("a single non-@ key is a record, not metadata")
	}
	if _, _, ok := singleMetaKey(map[string]any{"a": 1, "b": 2}); ok {
		t.Error("a multi-key object is a record, not metadata")
	}
}

func TestParseErrorReturnsLastErrorLine(t *testing.T) {
	stderr := []byte(`{"notice":"warming"}` + "\n" + `{"error":"boom","fixable_by":"agent"}` + "\n")
	obj := parseError(stderr)
	if obj == nil || obj["error"] != "boom" {
		t.Errorf("parseError should return the last error line, got %v", obj)
	}
}
