package finconfig

import "testing"

func TestLocalProtocolAlgorithmsMatchFrozenVectors(t *testing.T) {
	t.Parallel()

	records := []Record{
		{Key: "b", Values: map[string]string{"z": "2", "a": "1"}},
		{Key: "a", Values: map[string]string{"x": "true"}},
	}
	digest, err := computeBaseDigest(records)
	if err != nil {
		t.Fatal(err)
	}
	if digest != "70b79f915bffea02dbd2e1f92974af6c8d1b53585d89327a838cb5b2999e678d" {
		t.Fatalf("base digest = %s", digest)
	}
	empty, err := computeBaseDigest(nil)
	if err != nil {
		t.Fatal(err)
	}
	if empty != "4f53cda18c2baa0c0354bb5f9a3ecbe5ed12ab4d8e11ba873c2f11161202b945" {
		t.Fatalf("empty digest = %s", empty)
	}
	if _, err := computeBaseDigest([]Record{{Key: "a"}, {Key: "a"}}); err == nil {
		t.Fatal("duplicate record keys produced a digest")
	}
	nilValues, err := computeBaseDigest([]Record{{Key: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	emptyValues, err := computeBaseDigest([]Record{{Key: "a", Values: map[string]string{}}})
	if err != nil {
		t.Fatal(err)
	}
	if nilValues != emptyValues {
		t.Fatalf("nil and empty values produced different digests: %s != %s", nilValues, emptyValues)
	}

	bucket, err := clientBucket("consumer", "client")
	if err != nil {
		t.Fatal(err)
	}
	if bucket != 71 {
		t.Fatalf("client bucket = %d, want 71", bucket)
	}
	for _, identifiers := range [][2]string{{" consumer", "client"}, {"consumer", "client\x00suffix"}, {"", "client"}} {
		if _, err := clientBucket(identifiers[0], identifiers[1]); err == nil {
			t.Fatalf("invalid identifiers %q/%q produced a bucket", identifiers[0], identifiers[1])
		}
	}
}
