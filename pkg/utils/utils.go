package utils

import (
	"crypto/md5"
	"fmt"
	"io"
	"os"

	"github.com/docker/docker/client"
	"github.com/sirupsen/logrus"
)

// LogSetup sets up the logging for the application
func LogSetup(out io.Writer, level string) *logrus.Logger {
	lvl, err := logrus.ParseLevel(level)

	if err != nil {
		logrus.Warnf("Unable to use the %s level, %#v. Defaulting to warning.")
		lvl = logrus.WarnLevel
	}

	log := &logrus.Logger{
		Formatter: &logrus.TextFormatter{
			FullTimestamp:   true,
			TimestampFormat: "2006-01-02 15:15:10",
		},
		Out:          out,
		ReportCaller: false,
		Level:        lvl,
	}

	return log
}

// LookupEnvOrString looks up an environment variable if not found
// returns defaultVal
func LookupEnvOrString(envName, defaultVal string) string {
	if val, ok := os.LookupEnv(envName); ok {
		return val
	}

	return defaultVal
}

// Md5OfString returns the md5 has of the string
// Its ok to use md5 hashing here as it just used
// for consistent and sanitized naming
func Md5OfString(str string) string {
	return fmt.Sprintf("%x", md5.Sum([]byte(str)))
}

// DockerCliClient builds a Docker Cli Client to interact Docker
func DockerCliClient() (*client.Client, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return nil, err
	}
	return cli, nil
}
