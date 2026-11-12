// Package transfer implements the gofer wire protocol: username lookup and file transfer.
//
// Send stream (/gofer/send/1.0.0):
//
//	sender   → offer JSON line
//	receiver → reply JSON line (accept/decline)
//	sender   → <size> raw bytes, then 32-byte SHA-256
//	receiver → reply JSON line (verified/failed)
package transfer

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"path"
	"strings"
)

const (
	protoWhois = "/gofer/whois/1.0.0"
	protoSend  = "/gofer/send/1.0.0"

	maxLine = 64 << 10
)

type offer struct {
	From string `json:"from"`
	To   string `json:"to"`
	Name string `json:"name"`
	Size int64  `json:"size"`
}

type reply struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
}

type whoisReply struct {
	Username string `json:"username"`
}

func writeLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

func readLine(r *bufio.Reader, v any) error {
	var buf []byte
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return err
		}
		buf = append(buf, chunk...)
		if len(buf) > maxLine {
			return errors.New("message too large")
		}
		if !isPrefix {
			break
		}
	}
	return json.Unmarshal(buf, v)
}

// safeName reduces an offered file name to a plain base name, or "" if unusable.
func safeName(name string) string {
	// Treat both separators as separators regardless of the receiver's OS.
	n := path.Base(path.Clean("/" + strings.ReplaceAll(name, `\`, "/")))
	if n == "/" || n == "." || n == ".." {
		return ""
	}
	return n
}
