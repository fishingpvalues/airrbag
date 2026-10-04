package config

import (
	"errors"
	"runtime"
	"strings"
	"testing"
)

// Every shape must be judged on the decoded config, the same result the
// loader uses, never on the text.
func TestInlineSecretShapes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX permissions")
	}
	t.Setenv("AIRRBAG_T_A", "envvalue0123456789abcdef")
	keyFile := writeConfig(t, "filevalue0123456789abcdef", 0o600)
	head := "instances:\n  - name: radarr\n    listen: ':1'\n    upstream: http://radarr:7878\n"
	// Fake credentials are built at runtime so secret scanners do not flag
	// the fixtures.
	lit := strings.Repeat("ab", 11)
	pw := strings.Join([]string{"not", "a", "real", "pw"}, "-")
	cases := []struct {
		name   string
		body   string
		inline bool // true: a readable file must be refused
	}{
		{"env reference", head + "    api_key: ${AIRRBAG_T_A}\n", false},
		{"quoted env reference", head + "    api_key: \"${AIRRBAG_T_A}\"\n", false},
		{"file reference", head + "    api_key: ${file:" + keyFile + "}\n", false},
		{"plain literal", head + "    api_key: " + lit + "\n", true},
		{"single-quoted literal", head + "    api_key: '" + lit + "'\n", true},
		{"flow style", "instances: [{name: radarr, listen: ':1', upstream: 'http://radarr:7878', api_key: " + lit + "}]\n", true},
		{"literal block scalar", head + "    api_key: |\n      " + lit + "\n", true},
		{"folded block scalar", head + "    api_key: >-\n      " + lit + "\n", true},
		{"reference with suffix", head + "    api_key: ${AIRRBAG_T_A}tail\n", true},
		{"two references", head + "    api_key: ${AIRRBAG_T_A}${AIRRBAG_T_A}\n", true},
		{"reference in a block scalar with junk", head + "    api_key: |\n      ${AIRRBAG_T_A}\n      " + lit + "\n", true},
		{"anchor on a non-secret field, alias in the secret", head + "    api_key: ${AIRRBAG_T_A}\n" +
			"clients:\n  - name: q\n    type: qbittorrent\n    url: http://q:8080\n    username: &k " + pw + "\n    password: *k\n", true},
		{"anchored reference reused", head + "    api_key: &r ${AIRRBAG_T_A}\n" +
			"clients:\n  - name: SABnzbd\n    type: sabnzbd\n    url: http://sab:8080\n    api_key: *r\n", false},
		{"inline client password", head + "    api_key: ${AIRRBAG_T_A}\n" +
			"clients:\n  - name: q\n    type: qbittorrent\n    url: http://q:8080\n    password: " + pw + "\n", true},
		{"inline grant secret", head + "    api_key: ${AIRRBAG_T_A}\nauth:\n  grant_secret: " + strings.Repeat("g", 34) + "\n", true},
		{"password in a URL", head + "    api_key: ${AIRRBAG_T_A}\n" +
			"clients:\n  - name: q\n    type: qbittorrent\n    url: http://admin:" + pw + "@q:8080\n", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Load(writeConfig(t, c.body, 0o644))
			if c.inline {
				if !errors.Is(err, ErrInsecurePerms) {
					t.Fatalf("readable config with an inline secret loaded: %v", err)
				}
				// The same file at 0600 is fine (apart from validation).
				if _, err := Load(writeConfig(t, c.body, 0o600)); errors.Is(err, ErrInsecurePerms) {
					t.Fatalf("0600 must not be refused for permissions: %v", err)
				}
			} else if err != nil {
				t.Fatalf("reference-only config refused: %v", err)
			}
		})
	}
}

func TestDuplicateKeysAndMultiDocRefused(t *testing.T) {
	lit := strings.Repeat("ab", 11)
	head := "instances:\n  - name: radarr\n    listen: ':1'\n    upstream: http://radarr:7878\n"
	for name, body := range map[string]string{
		"duplicate key":   head + "    api_key: ${AIRRBAG_T_A}\n    api_key: " + lit + "\n",
		"second document": head + "    api_key: ${AIRRBAG_T_A}\n---\n" + head + "    api_key: " + lit + "\n",
	} {
		if _, err := Load(writeConfig(t, body, 0o600)); err == nil {
			t.Errorf("%s: must not load", name)
		}
	}
}

func TestEnvValueCannotInjectYAML(t *testing.T) {
	t.Setenv("AIRRBAG_T_EVIL", "x\nguard:\n  enabled: false")
	head := "instances:\n  - name: radarr\n    listen: ':1'\n    upstream: http://radarr:7878\n"
	c, err := Load(writeConfig(t, head+"    api_key: ${AIRRBAG_T_EVIL}\n", 0o600))
	if err != nil {
		t.Fatal(err)
	}
	if !c.Guard.GuardEnabled() || !strings.Contains(c.Instances[0].APIKey, "guard:") {
		t.Fatal("an environment value restructured the config")
	}
}
