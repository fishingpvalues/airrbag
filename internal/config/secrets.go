package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"

	"filippo.io/age"
	"filippo.io/age/armor"
)

// Secret references in the config file:
//
//	${NAME}             the environment variable NAME, or, when NAME is unset
//	                    and NAME_FILE is set, the contents of that file
//	                    (the Docker / Kubernetes secrets convention)
//	${file:/run/x/key}  the contents of a file
//
// File contents lose one trailing newline. A reference that resolves to
// nothing is left empty, and validation then reports the missing secret.
var secretRef = regexp.MustCompile(`\$\{(file:[^}]+|[A-Za-z_][A-Za-z0-9_]*)\}`)

// ExpandSecrets resolves every ${...} reference in s.
func ExpandSecrets(s string) (string, error) {
	var errs []error
	out := secretRef.ReplaceAllStringFunc(s, func(m string) string {
		ref := secretRef.FindStringSubmatch(m)[1]
		if path, ok := strings.CutPrefix(ref, "file:"); ok {
			v, err := readSecretFile(path)
			if err != nil {
				errs = append(errs, err)
			}
			return v
		}
		if v, ok := os.LookupEnv(ref); ok && v != "" {
			return v
		}
		if path := os.Getenv(ref + "_FILE"); path != "" {
			v, err := readSecretFile(path)
			if err != nil {
				errs = append(errs, err)
			}
			return v
		}
		return ""
	})
	return out, errors.Join(errs...)
}

// Expand is ExpandSecrets without error reporting (kept for callers that
// only need environment expansion).
func Expand(s string) string {
	out, _ := ExpandSecrets(s)
	return out
}

func readSecretFile(path string) (string, error) {
	b, err := os.ReadFile(path) //nolint:gosec // operator-chosen secret path
	if err != nil {
		return "", fmt.Errorf("secret file %s: %w", path, err)
	}
	return strings.TrimSuffix(strings.TrimSuffix(string(b), "\n"), "\r"), nil
}

// InsecurePermsEnv lets Airrbag start with a config file that other users
// can read (for filesystems without POSIX permissions). It only downgrades
// the refusal to a warning.
const InsecurePermsEnv = "AIRRBAG_ALLOW_INSECURE_CONFIG"

// AgeIdentityEnv names the file holding the age identity used to decrypt an
// encrypted config (airrbag.yml.age).
const AgeIdentityEnv = "AIRRBAG_AGE_IDENTITY_FILE"

// ErrInsecurePerms is returned when the config file is readable by group or
// others while it may contain secrets.
var ErrInsecurePerms = errors.New("config file is readable by group or others; chmod 600 it (or set " + InsecurePermsEnv + "=1)")

// checkPerms refuses a config file that group or others can read. The file
// holds, or points at, every API key and password Airrbag uses.
func checkPerms(path string) (warn error, err error) {
	if runtime.GOOS == "windows" {
		return nil, nil
	}
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.Mode().Perm()&0o077 == 0 {
		return nil, nil
	}
	e := fmt.Errorf("%s (mode %04o): %w", path, st.Mode().Perm(), ErrInsecurePerms)
	if os.Getenv(InsecurePermsEnv) == "1" {
		return e, nil
	}
	return nil, e
}

var ageHeader = []byte("age-encryption.org/v1")

// maybeDecrypt returns raw unchanged unless it is an age file, in which case
// it is decrypted with the identity named by AIRRBAG_AGE_IDENTITY_FILE.
func maybeDecrypt(raw []byte) ([]byte, error) {
	if !bytes.HasPrefix(raw, ageHeader) && !bytes.HasPrefix(raw, []byte("-----BEGIN AGE ENCRYPTED FILE-----")) {
		return raw, nil
	}
	idPath := os.Getenv(AgeIdentityEnv)
	if idPath == "" {
		return nil, fmt.Errorf("config is age-encrypted; set %s to the identity file", AgeIdentityEnv)
	}
	f, err := os.Open(idPath) //nolint:gosec // operator-chosen identity path
	if err != nil {
		return nil, fmt.Errorf("age identity: %w", err)
	}
	defer func() { _ = f.Close() }()
	ids, err := age.ParseIdentities(f)
	if err != nil {
		return nil, fmt.Errorf("age identity: %w", err)
	}
	var src io.Reader = bytes.NewReader(raw)
	if bytes.HasPrefix(raw, []byte("-----BEGIN")) {
		src = armor.NewReader(bytes.NewReader(raw))
	}
	r, err := age.Decrypt(src, ids...)
	if err != nil {
		return nil, fmt.Errorf("decrypt config: %w", err)
	}
	return io.ReadAll(io.LimitReader(r, 4<<20))
}

// placeholders are values that are clearly a template left unfilled.
var placeholders = []string{
	"changeme", "change_me", "change-me", "changethis", "change_this",
	"your_api_key", "yourapikey", "your-api-key", "apikey", "api_key",
	"password", "secret", "xxx", "xxxx", "todo", "replace_me", "replaceme",
}

// IsPlaceholder reports whether v looks like an unfilled template value.
func IsPlaceholder(v string) bool {
	t := strings.ToLower(strings.TrimSpace(v))
	if t == "" {
		return false
	}
	if strings.HasPrefix(t, "<") && strings.HasSuffix(t, ">") {
		return true
	}
	if strings.HasPrefix(t, "${") {
		return true // an unresolved reference survived expansion
	}
	if strings.HasPrefix(t, "change_this") || strings.HasPrefix(t, "your_") {
		return true
	}
	for _, p := range placeholders {
		if t == p {
			return true
		}
	}
	return false
}

// Secrets lists every secret value in the config, longest first, so a
// scrubber replaces "abcdef" before its prefix "abc".
func (c *Config) Secrets() []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		// Very short values would scrub ordinary words; they are rejected by
		// validation for API keys anyway.
		if len(s) < 6 || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, in := range c.Instances {
		add(in.APIKey)
	}
	for _, cl := range c.Clients {
		add(cl.Password)
		add(cl.APIKey)
	}
	add(c.Auth.GrantSecret)
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}
