package config

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"filippo.io/age"
)

const minimal = "instances:\n  - name: radarr\n    listen: ':1'\n    upstream: http://radarr:7878\n    api_key: %s\n"

func writeConfig(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "airrbag.yml")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSecretFromFileEnv(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(keyFile, []byte("filekey0123456789abcdef\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AIRRBAG_T_KEY_FILE", keyFile)
	p := writeConfig(t, strings.Replace(minimal, "%s", "${AIRRBAG_T_KEY}", 1), 0o600)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Instances[0].APIKey != "filekey0123456789abcdef" {
		t.Errorf("NAME_FILE not honored (or newline kept): %q", c.Instances[0].APIKey)
	}
}

func TestSecretFileReference(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(keyFile, []byte("refkey0123456789abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := writeConfig(t, strings.Replace(minimal, "%s", "${file:"+keyFile+"}", 1), 0o600)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Instances[0].APIKey != "refkey0123456789abcdef" {
		t.Errorf("${file:} not resolved: %q", c.Instances[0].APIKey)
	}
	missing := writeConfig(t, strings.Replace(minimal, "%s", "${file:/does/not/exist}", 1), 0o600)
	if _, err := Load(missing); err == nil {
		t.Error("a missing secret file must fail loudly")
	}
}

func TestRefusesReadableConfig(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX permissions")
	}
	p := writeConfig(t, strings.Replace(minimal, "%s", "abcdef0123456789abcdef", 1), 0o644)
	if _, err := Load(p); !errors.Is(err, ErrInsecurePerms) {
		t.Fatalf("0644 config with an inline key must be refused, got %v", err)
	}
	// Only ${...} references: nothing to hide, readable is fine.
	t.Setenv("AIRRBAG_T_REF", "refonly0123456789abcdef")
	refs := writeConfig(t, strings.Replace(minimal, "%s", "${AIRRBAG_T_REF}", 1), 0o644)
	if c, err := Load(refs); err != nil || c.Instances[0].APIKey != "refonly0123456789abcdef" {
		t.Fatalf("reference-only config must load even when readable: %v", err)
	}
	t.Setenv(InsecurePermsEnv, "1")
	c, warns, err := LoadWithWarnings(p)
	if err != nil || c == nil || len(warns) != 1 {
		t.Fatalf("override should downgrade to a warning: %v %v", warns, err)
	}
}

func TestPlaceholdersRejected(t *testing.T) {
	for _, v := range []string{"changeme", "<your api key>", "your_api_key", "CHANGE_THIS_radarr", "xxx"} {
		if !IsPlaceholder(v) {
			t.Errorf("%q should be a placeholder", v)
		}
	}
	for _, v := range []string{strings.Repeat("3f9c", 8), "pa$$w0rd!x"} {
		if IsPlaceholder(v) {
			t.Errorf("%q is a real secret", v)
		}
	}
	p := writeConfig(t, strings.Replace(minimal, "%s", "changeme", 1), 0o600)
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), "placeholder") {
		t.Errorf("placeholder api_key must be rejected: %v", err)
	}
}

func TestAgeEncryptedConfig(t *testing.T) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	var enc bytes.Buffer
	w, err := age.Encrypt(&enc, id.Recipient())
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte(strings.Replace(minimal, "%s", "agekey0123456789abcdef", 1)))
	_ = w.Close()
	p := filepath.Join(t.TempDir(), "airrbag.yml.age")
	if err := os.WriteFile(p, enc.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err == nil || !strings.Contains(err.Error(), AgeIdentityEnv) {
		t.Fatalf("without an identity the error must name %s: %v", AgeIdentityEnv, err)
	}
	idFile := filepath.Join(t.TempDir(), "id.txt")
	if err := os.WriteFile(idFile, []byte(id.String()+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(AgeIdentityEnv, idFile)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Instances[0].APIKey != "agekey0123456789abcdef" {
		t.Errorf("decrypted key = %q", c.Instances[0].APIKey)
	}
}

func TestSecretsListLongestFirst(t *testing.T) {
	c := &Config{
		Instances: []Instance{{APIKey: "abcdef"}, {APIKey: "abcdefghijkl"}},
		Clients:   []Client{{Password: "pw123456"}, {APIKey: "abc"}},
	}
	got := c.Secrets()
	if len(got) != 3 || got[0] != "abcdefghijkl" {
		t.Errorf("secrets = %v (short values are skipped, longest first)", got)
	}
}

func TestAuthValidation(t *testing.T) {
	base := strings.Replace(minimal, "%s", "abcdef0123456789abcdef", 1)
	for _, tc := range []struct {
		extra string
		ok    bool
	}{
		{"auth:\n  trusted_proxies: [172.22.0.1, 10.0.0.0/8]\n", true},
		{"auth:\n  trusted_proxies: [not-an-ip]\n", false},
		{"auth:\n  forward_auth_header: Remote-User\n", false},
		{"auth:\n  forward_auth_header: Remote-User\n  trusted_proxies: [172.22.0.1]\n", true},
		{"auth:\n  grant_secret: short\n", false},
		{"guard:\n  grant_ttl: 1h\n", false},
	} {
		_, err := Parse([]byte(base + tc.extra))
		if (err == nil) != tc.ok {
			t.Errorf("%q: err=%v, want ok=%v", tc.extra, err, tc.ok)
		}
	}
}
