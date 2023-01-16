package drone

import (
	"io"
	"strings"

	"github.com/bfontaine/jsons"
)

type jsonlogger struct {
	name   string
	number int
	writer *jsons.FileWriter
	seq    *sequence
}

// Write implements io.WriteCloser
func (j *jsonlogger) Write(b []byte) (n int, err error) {
	for _, part := range split(b) {
		if err := j.writer.Add(
			map[string]interface{}{
				"stepNumber": j.number,
				"stepName":   j.name,
				"line":       part,
			}); err != nil {
			return len(b), err
		}
	}
	return len(b), nil
}

// Close implements io.WriteCloser
func (j *jsonlogger) Close() error {
	log.Infoln("Closing")
	return nil
}

func split(b []byte) []string {
	s := string(b)
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

var _ io.WriteCloser = (*jsonlogger)(nil)
