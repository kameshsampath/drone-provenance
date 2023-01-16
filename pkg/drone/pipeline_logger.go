package drone

import (
	"context"
	"fmt"
	"io"
	"path"

	"github.com/bfontaine/jsons"
	"github.com/drone/drone-go/drone"
	"github.com/drone/runner-go/pipeline"
)

type jSONFileStreamer struct {
	seq     *sequence
	col     *sequence
	logFile string
	writer  *jsons.FileWriter
}

var _ pipeline.Streamer = (*jSONFileStreamer)(nil)

func newStreamer(pipelineID string) (*jSONFileStreamer, error) {
	logFile := path.Join(droneCILogsDir, fmt.Sprintf("%s.log", pipelineID))
	fw := jsons.NewFileWriter(logFile)
	if err := fw.Open(); err != nil {
		return nil, err
	}
	return &jSONFileStreamer{
		seq:     new(sequence),
		col:     new(sequence),
		logFile: logFile,
		writer:  fw,
	}, nil
}

// Stream implements pipeline.Streamer
func (j *jSONFileStreamer) Stream(_ context.Context, state *pipeline.State, name string) io.WriteCloser {
	var c *drone.Step
	for _, s := range state.Stage.Steps {
		if s.Name == name {
			c = s
			break
		}
	}
	return &jsonlogger{
		writer: j.writer,
		seq:    j.seq,
		name:   c.Name,
		number: c.Number,
	}
}
