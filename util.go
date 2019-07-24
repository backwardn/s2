package s2

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
)

// WriteError serializes an error to a response as XML
func WriteError(logger *logrus.Entry, w http.ResponseWriter, r *http.Request, err error) {
	switch e := err.(type) {
	case *Error:
		writeXML(logger, w, r, e.HTTPStatus, e)
	default:
		s3Err := InternalError(r, e)
		writeXML(logger, w, r, s3Err.HTTPStatus, s3Err)
	}
}

// writeXMLPrelude writes the HTTP headers and XML header to the response
func writeXMLPrelude(w http.ResponseWriter, r *http.Request, code int) {
	vars := mux.Vars(r)
	requestID := vars["requestID"]

	w.Header().Set("Content-Type", "application/xml")
	w.Header().Set("x-amz-id-2", requestID)
	w.Header().Set("x-amz-request-id", requestID)
	w.WriteHeader(code)
	fmt.Fprint(w, xml.Header)
}

// writeXMLBody writes the marshaled XML payload of a value
func writeXMLBody(logger *logrus.Entry, w http.ResponseWriter, v interface{}) {
	encoder := xml.NewEncoder(w)
	if err := encoder.Encode(v); err != nil {
		// just log a message since a response has already been partially
		// written
		logger.Errorf("could not encode xml response: %v", err)
	}
}

// writeXML writes HTTP headers, the XML header, and the XML payload to the
// response
func writeXML(logger *logrus.Entry, w http.ResponseWriter, r *http.Request, code int, v interface{}) {
	writeXMLPrelude(w, r, code)
	writeXMLBody(logger, w, v)
}

// NotImplementedEndpoint creates an endpoint that returns
// `NotImplementedError` responses. This can be used in places expecting a
// `HandlerFunc`, e.g. mux middleware.
func NotImplementedEndpoint(logger *logrus.Entry) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		WriteError(logger, w, r, NotImplementedError(r))
	}
}

// intFormValue extracts an int value from a request's form values, ensuring
// it's within specified bounds. If the value is unspecified, `def` is
// returned. If the value is not an int, or not with the specified bounds, an
// error is returned.
func intFormValue(r *http.Request, name string, min int, max int, def int) (int, error) {
	s := r.FormValue(name)
	if s == "" {
		return def, nil
	}

	i, err := strconv.Atoi(s)
	if err != nil || i < min || i > max {
		return 0, InvalidArgumentError(r)
	}

	return i, nil
}

//stripETagQuotes removes leading and trailing quotes in a string (if they
// exist.) This is used for ETags.
func stripETagQuotes(s string) string {
	if strings.HasPrefix(s, "\"") && strings.HasSuffix(s, "\"") {
		return strings.Trim(s, "\"")
	}
	return s
}

// addETagQuotes ensures that a given string has leading and trailing quotes.
// This is used for ETags.
func addETagQuotes(s string) string {
	if !strings.HasPrefix(s, "\"") {
		return fmt.Sprintf("\"%s\"", s)
	}
	return s
}

func normURI(uri string) string {
	parts := strings.Split(uri, "/")
	for i := range parts {
		parts[i] = encodePathFrag(parts[i])
	}
	return strings.Join(parts, "/")
}

func encodePathFrag(s string) string {
	hexCount := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if shouldEscape(c) {
			hexCount++
		}
	}
	t := make([]byte, len(s)+2*hexCount)
	j := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if shouldEscape(c) {
			t[j] = '%'
			t[j+1] = "0123456789ABCDEF"[c>>4]
			t[j+2] = "0123456789ABCDEF"[c&15]
			j += 3
		} else {
			t[j] = c
			j++
		}
	}
	return string(t)
}

func shouldEscape(c byte) bool {
	if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' {
		return false
	}
	if '0' <= c && c <= '9' {
		return false
	}
	if c == '-' || c == '_' || c == '.' || c == '~' {
		return false
	}
	return true
}

func normQuery(v url.Values) string {
	queryString := v.Encode()

	// Go encodes a space as '+' but Amazon requires '%20'. Luckily any '+' in the
	// original query string has been percent escaped so all '+' chars that are left
	// were originally spaces.

	return strings.Replace(queryString, "+", "%20", -1)
}

func hmacSHA1(key []byte, content string) []byte {
	mac := hmac.New(sha1.New, key)
	mac.Write([]byte(content))
	return mac.Sum(nil)
}

func hmacSHA256(key []byte, content string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(content))
	return mac.Sum(nil)
}

func requireContentLength(r *http.Request) error {
	contentLength := r.Header.Get("content-length")
	if contentLength == "" {
		return MissingContentLengthError(r)
	}
	return nil
}
