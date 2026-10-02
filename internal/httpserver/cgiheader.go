package httpserver

import (
	"bufio"
	"errors"
	"io"
	"net/http"
)

type headerReader struct {
	r      *bufio.Reader
	Header http.Header
	Status int
}

func newHeaderReader(r io.Reader) *headerReader {
	return &headerReader{r: bufio.NewReader(r), Header: http.Header{}}
}

func (h *headerReader) ReadHeaders() error {
	for {
		line, err := h.r.ReadString('\n')
		if err != nil {
			return err
		}
		line = trimCRLF(line)
		if line == "" {
			return nil
		}
		k, v, ok := splitColon(line)
		if !ok {
			return errors.New("malformed CGI header")
		}
		h.Header.Add(k, v)
	}
}

func (h *headerReader) Read(p []byte) (int, error) {
	return h.r.Read(p)
}
