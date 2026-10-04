package proxy

import (
	"bytes"
	"net/http"
)

// setBaseSecurityHeaders applies to every response Airrbag answers itself
// (never to the *Arr's own pages, whose headers pass through unchanged).
func setBaseSecurityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	h.Set("Permissions-Policy", "accelerometer=(), camera=(), geolocation=(), gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=(), interest-cohort=()")
}

// scrubWriter scrubs a text body (metrics) before it is sent.
type scrubWriter struct {
	http.ResponseWriter
	buf  bytes.Buffer
	code int
}

func (s *scrubWriter) WriteHeader(code int) { s.code = code }

func (s *scrubWriter) Write(b []byte) (int, error) { return s.buf.Write(b) }

// Flush sends the scrubbed body; the metrics handler writes in one go, so
// it is called once from ServeHTTP's caller via finish.
func (s *scrubWriter) finish() {
	if s.code == 0 {
		s.code = http.StatusOK
	}
	s.ResponseWriter.WriteHeader(s.code)
	_, _ = s.ResponseWriter.Write(Scrubber.Bytes(s.buf.Bytes()))
}
