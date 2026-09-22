package content

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDir lays out a content directory from a name-to-body map and returns
// its path.
func writeDir(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	return dir
}

const citiesYML = `version: 1
cities:
  - code: alpha
    name: Alpha
    tax_rate_bps: 500
    cost_of_living: 1000
  - code: bravo
    name: Bravo
    tax_rate_bps: 750
    cost_of_living: 2000
`

const routesYML = `version: 1
routes:
  - {from: alpha, to: bravo, distance: 100}
`

func TestLoadReadsEveryFileInTheDirectory(t *testing.T) {
	dir := writeDir(t, map[string]string{
		"cities.yml": citiesYML,
		"routes.yml": routesYML,
		"skills.yml": "version: 1\nskills:\n  - {code: driving, name: Driving, category: technical}\n",
	})

	pack, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if pack.Schema != 1 {
		t.Errorf("schema version is %d, want 1", pack.Schema)
	}
	if len(pack.Cities) != 2 || len(pack.Routes) != 1 || len(pack.Skills) != 1 {
		t.Fatalf("got %d cities, %d routes, %d skills; want 2, 1, 1",
			len(pack.Cities), len(pack.Routes), len(pack.Skills))
	}
	if pack.Checksum == "" {
		t.Error("Load produced no checksum")
	}
	// Version is the load number the database assigns. A pack that has never
	// been applied has not got one.
	if pack.Version != 0 {
		t.Errorf("a freshly parsed pack claims version %d, want 0", pack.Version)
	}
	if err := pack.Validate(); err != nil {
		t.Errorf("the parsed pack does not validate: %v", err)
	}
}

// The directory layout is a convention, not a rule: one struct covers every
// file, so a key put in the wrong file still loads.
func TestLoadDoesNotCareWhichFileAKeyIsIn(t *testing.T) {
	dir := writeDir(t, map[string]string{
		"everything.yml": citiesYML + strings.TrimPrefix(routesYML, "version: 1\n"),
	})
	pack, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(pack.Cities) != 2 || len(pack.Routes) != 1 {
		t.Fatalf("got %d cities and %d routes, want 2 and 1", len(pack.Cities), len(pack.Routes))
	}
}

// TestLoadRejectsAnUnknownField is the single most valuable thing Load does.
// Without KnownFields(true), `tax_rate_bsp: 750` would load as a tax rate of
// zero and nobody would find out until a city stopped collecting tax.
//
// The message is asserted, not just the error kind: the author needs the file,
// the line and the key, and an error naming an internal Go type instead is
// useless to whoever edits yaml.
func TestLoadRejectsAnUnknownField(t *testing.T) {
	dir := writeDir(t, map[string]string{
		"cities.yml": `version: 1
cities:
  - code: alpha
    name: Alpha
    tax_rate_bsp: 750
    cost_of_living: 1000
`,
	})

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load accepted a misspelled key")
	}
	if !errors.Is(err, ErrUnknownField) {
		t.Fatalf("got %v, want an ErrUnknownField", err)
	}

	msg := err.Error()
	t.Logf("unknown-field error: %s", msg)

	for _, want := range []string{"cities.yml", "line 5", "tax_rate_bsp"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the error does not mention %q:\n%s", want, msg)
		}
	}
	// The internal Go type name is noise to a content author and must not
	// survive into the message.
	if strings.Contains(msg, "content.file") {
		t.Errorf("the error leaks an internal type name:\n%s", msg)
	}
}

func TestLoadRejectsAWrongTypedValue(t *testing.T) {
	dir := writeDir(t, map[string]string{
		"cities.yml": `version: 1
cities:
  - code: alpha
    name: Alpha
    tax_rate_bps: high
    cost_of_living: 1000
`,
	})

	_, err := Load(dir)
	if err == nil {
		t.Fatal("Load accepted a non-numeric tax rate")
	}
	if errors.Is(err, ErrUnknownField) {
		t.Errorf("a bad value was reported as an unknown field: %v", err)
	}
	if !strings.Contains(err.Error(), "cities.yml") {
		t.Errorf("the error does not name the file: %v", err)
	}
}

func TestLoadRejectsAnEmptyDirectory(t *testing.T) {
	// A .yaml file is deliberately invisible: allowing both spellings would
	// let a file sit in the directory being edited and never loaded.
	dir := writeDir(t, map[string]string{"cities.yaml": citiesYML})

	_, err := Load(dir)
	if !errors.Is(err, ErrNoContentFiles) {
		t.Fatalf("got %v, want ErrNoContentFiles", err)
	}
}

func TestLoadRejectsAMissingDirectory(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "does-not-exist"))
	if !errors.Is(err, ErrNoContentFiles) {
		t.Fatalf("got %v, want ErrNoContentFiles", err)
	}
}

// Only the first document of a file would be read, so everything after the ---
// would be silently dropped.
func TestLoadRejectsASecondDocument(t *testing.T) {
	dir := writeDir(t, map[string]string{
		"cities.yml": citiesYML + "---\n" + `cities:
  - code: charlie
    name: Charlie
    tax_rate_bps: 100
    cost_of_living: 100
`,
	})

	_, err := Load(dir)
	if !errors.Is(err, ErrMultipleDocuments) {
		t.Fatalf("got %v, want ErrMultipleDocuments", err)
	}
}

func TestLoadRejectsDisagreeingSchemaVersions(t *testing.T) {
	dir := writeDir(t, map[string]string{
		"cities.yml": citiesYML,
		"routes.yml": "version: 2\nroutes: []\n",
	})

	_, err := Load(dir)
	if !errors.Is(err, ErrVersionMismatch) {
		t.Fatalf("got %v, want ErrVersionMismatch", err)
	}
	if !strings.Contains(err.Error(), "routes.yml") {
		t.Errorf("the error does not name the disagreeing file: %v", err)
	}
}

// A content type that has not been authored yet is a normal state, so an empty
// placeholder must behave exactly like its absence.
func TestLoadAcceptsAnEmptyFile(t *testing.T) {
	dir := writeDir(t, map[string]string{
		"cities.yml": citiesYML,
		"routes.yml": routesYML,
		"skills.yml": "",
	})

	pack, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(pack.Skills) != 0 {
		t.Errorf("an empty file produced %d skills", len(pack.Skills))
	}
}

// Determinism is what makes the checksum mean anything: the same directory
// must produce the same pack and the same digest on every machine.
func TestLoadIsDeterministic(t *testing.T) {
	files := map[string]string{"cities.yml": citiesYML, "routes.yml": routesYML}

	first, err := Load(writeDir(t, files))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	second, err := Load(writeDir(t, files))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if first.Checksum != second.Checksum {
		t.Errorf("two loads of identical files disagree:\n%s\n%s", first.Checksum, second.Checksum)
	}
	if first.Cities[0].Code != "alpha" || first.Cities[1].Code != "bravo" {
		t.Errorf("entries are not in file order: %+v", first.Cities)
	}
}

// The file NAME is part of the digest, so splitting or renaming a file changes
// it even when the loaded world is identical. The checksum identifies a
// source, not a world.
func TestChecksumFollowsTheFileNames(t *testing.T) {
	same, err := Load(writeDir(t, map[string]string{"cities.yml": citiesYML}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	renamed, err := Load(writeDir(t, map[string]string{"world.yml": citiesYML}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if same.Checksum == renamed.Checksum {
		t.Error("renaming a file did not change the checksum")
	}
	if len(same.Cities) != len(renamed.Cities) {
		t.Error("renaming a file changed the content")
	}
}

func TestLoadChecksumChangesWithContent(t *testing.T) {
	before, err := Load(writeDir(t, map[string]string{"cities.yml": citiesYML}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	after, err := Load(writeDir(t, map[string]string{
		"cities.yml": strings.Replace(citiesYML, "tax_rate_bps: 500", "tax_rate_bps: 501", 1),
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if before.Checksum == after.Checksum {
		t.Error("changing a tax rate did not change the checksum")
	}
}
