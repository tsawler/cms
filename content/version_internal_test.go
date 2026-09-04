package content

// The payload format's compatibility rule, which no database test can
// reach: a stored edition outlives the build that wrote it, and the two
// directions are not symmetric. An older payload has to keep working
// forever; a newer one must be refused rather than silently read short.

import (
	"strconv"
	"testing"
)

func TestDecodeSnapshotReadsOlderFormats(t *testing.T) {
	// Format 1, exactly as step 2 of this feature wrote it: no code.
	const v1 = `{"v":1,"page":{"template_name":"about.gohtml","head_css":"","body_js":""},` +
		`"meta":[{"locale":"en","title":"About","description":"","meta_description":""}],` +
		`"blocks":[{"region":"main","locale":"en","sort":0,"kind":"html","content":"<p>hi</p>"}]}`

	snap, err := decodeSnapshot(7, v1)
	if err != nil {
		t.Fatalf("decodeSnapshot(format 1): %v", err)
	}
	if snap.V != 1 {
		t.Errorf("V = %d, want the format it was written in", snap.V)
	}
	if snap.Page.TemplateName != "about.gohtml" {
		t.Errorf("template = %q, want it read as before", snap.Page.TemplateName)
	}
	if len(snap.Blocks) != 1 || snap.Blocks[0].Content != "<p>hi</p>" {
		t.Errorf("blocks = %+v, want the stored block", snap.Blocks)
	}
	// The field format 1 never had reads as absent, which is what "this
	// edition predates code freezing" should mean — not an error, and not
	// a claim that the page named no code blocks.
	if snap.Code != nil {
		t.Errorf("Code = %+v, want nil for a payload written before it existed", snap.Code)
	}
}

func TestDecodeSnapshotRefusesNewerFormats(t *testing.T) {
	// A build older than the database it is pointed at: reading this with
	// the current struct would drop whatever the newer format added, and
	// on a restore that means losing content rather than failing.
	const future = `{"v":99,"page":{},"meta":[],"blocks":[]}`
	if _, err := decodeSnapshot(7, future); err == nil {
		t.Error("decodeSnapshot accepted a payload from a newer format")
	}
}

func TestDecodeSnapshotRejectsMalformedPayloads(t *testing.T) {
	for name, payload := range map[string]string{
		"not json":        `{"v":2,`,
		"no format":       `{"page":{},"meta":[],"blocks":[]}`,
		"zero format":     `{"v":0,"page":{},"meta":[],"blocks":[]}`,
		"negative format": `{"v":-1,"page":{},"meta":[],"blocks":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeSnapshot(7, payload); err == nil {
				t.Errorf("decodeSnapshot(%s) returned no error", name)
			}
		})
	}
}

// The current format round-trips, so the constant and the struct agree.
func TestDecodeSnapshotReadsTheCurrentFormat(t *testing.T) {
	snap, err := decodeSnapshot(7, `{"v":`+strconv.Itoa(snapshotVersion)+`,"page":{},"meta":[],`+
		`"blocks":[],"code":[{"key":"signup","name":"Signup","html":"<b>x</b>"}]}`)
	if err != nil {
		t.Fatalf("decodeSnapshot(current): %v", err)
	}
	if len(snap.Code) != 1 || snap.Code[0].Key != "signup" || snap.Code[0].HTML != "<b>x</b>" {
		t.Errorf("Code = %+v, want the frozen block", snap.Code)
	}
}
