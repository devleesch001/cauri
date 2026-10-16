package cowrie

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

// The lines of a session against the official cowrie/cowrie image (revision
// 56b2abb): a public key offered then a password, LANG sent, an unknown
// command and a password change.
const capture = "../ingest/testdata/cowrie-56b2abb-fragments.json"

func TestFragment(t *testing.T) {
	want := map[string]string{
		Connect:       "",
		ClientVersion: "",
		ClientKex:     "",
		"cowrie.client.fingerprint": `{"fingerprint":"cd:7a:7c:bb:ca:55:fb:e7:28:5c:e1:4f:85:3d:b4:3c",` +
			`"key":"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINLbpaL9zzV7Zb+vBtTgV8wrLw3BmJ10i5Y4Rv+4UAvg","type":"ssh-ed25519","username":"root"}`,
		// A public key login has no password: the key stays.
		LoginFailed: `{"fingerprint":"cd:7a:7c:bb:ca:55:fb:e7:28:5c:e1:4f:85:3d:b4:3c",` +
			`"key":"ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAINLbpaL9zzV7Zb+vBtTgV8wrLw3BmJ10i5Y4Rv+4UAvg","type":"ssh-ed25519"}`,
		LoginSuccess:              "",
		"cowrie.client.var":       `{"name":"LANG","value":"fr_FR.UTF-8"}`,
		"cowrie.session.params":   `{"arch":"linux-x64-lsb"}`,
		CommandInput:              "",
		"cowrie.command.failed":   `{"input":"foobar "}`,
		"cowrie.command.chpasswd": `{"realm":"chpasswd","username":"root"}`,
		// A session keeps the name and duration of its last recording only.
		LogClosed: `{"duplicate":false,"duration_ms":60058,"shasum":"762a07f4935a4ee1e53c9be41baeeebe7da22052d0dbb1c4dcc5c08944489d5e",` +
			`"size":68,"ttylog":"var/lib/cowrie/tty/762a07f4935a4ee1e53c9be41baeeebe7da22052d0dbb1c4dcc5c08944489d5e"}`,
		SessionClosed: "",
	}
	f, err := os.Open(capture)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	seen := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		fields, ok := Parse(sc.Bytes())
		if !ok {
			t.Fatalf("not an object: %s", sc.Text())
		}
		var id string
		_ = json.Unmarshal(fields["eventid"], &id)
		w, ok := want[id]
		if !ok {
			t.Errorf("%s: not expected", id)
			continue
		}
		seen++
		got := ""
		if d := Fragment(fields, id, true); d != nil {
			got = *d
		}
		if got != w {
			t.Errorf("%s:\n got %s\nwant %s", id, got, w)
		}
	}
	if seen != len(want) {
		t.Errorf("%d events read, want %d", seen, len(want))
	}
}

// What a session holds stays when the line has none; a projected field of
// the wrong type is not in its column, so it stays too.
func TestFragmentKeepsWhatIsNotStored(t *testing.T) {
	f, _ := Parse([]byte(`{"eventid":"cowrie.login.failed","session":"","src_ip":"203.0.113.7","username":42,"password":"x"}`))
	if d := Fragment(f, LoginFailed, false); d == nil || *d != `{"src_ip":"203.0.113.7","username":42}` {
		t.Errorf("fragment = %v", deref(d))
	}
	if _, ok := Parse([]byte(`["not an object"]`)); ok {
		t.Error("an array parsed")
	}
}

func TestParseKex(t *testing.T) {
	line := func(keyAlgs string) Fields {
		f, _ := Parse([]byte(`{"eventid":"cowrie.client.kex","hassh":"eeca2460550b9ded084ecf2f70a75356",` +
			`"hasshAlgorithms":"k1,k2;e1;m1;none","kexAlgs":["k1","k2"],"encCS":["e1"],"macCS":["m1"],"compCS":["none"],` +
			`"keyAlgs":[` + keyAlgs + `],"langCS":[""]}`))
		return f
	}
	a, b := ParseKex(line(`"ssh-ed25519"`)), ParseKex(line(`"ecdsa-sha2-nistp256"`))
	if deref(a.HASSH) != "eeca2460550b9ded084ecf2f70a75356" || deref(a.Algorithms) != "k1,k2;e1;m1;none" ||
		deref(a.KeyAlgs) != "ssh-ed25519" || deref(a.LangAlgs) != "" || len(a.Digest) != 32 {
		t.Errorf("kex = %+v", a)
	}
	// Same client, other host key algorithms: another combination.
	if bytes.Equal(a.Digest, b.Digest) || !bytes.Equal(a.Digest, ParseKex(line(`"ssh-ed25519"`)).Digest) {
		t.Error("the digest must follow every list, and only them")
	}
	if h := HASSHOnly("eeca2460550b9ded084ecf2f70a75356"); bytes.Equal(h.Digest, a.Digest) || deref(h.HASSH) != deref(a.HASSH) {
		t.Errorf("HASSH only = %+v", h)
	}
	// Without the lists, the algorithms Cowrie joined.
	f, _ := Parse([]byte(`{"hassh":"x","hasshAlgorithms":"a;b;c;d"}`))
	if k := ParseKex(f); deref(k.Algorithms) != "a;b;c;d" || k.KeyAlgs != nil {
		t.Errorf("kex without lists = %+v", k)
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}
