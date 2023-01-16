package drone

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/client"
	"github.com/drone-runners/drone-runner-docker/engine"
	"github.com/drone-runners/drone-runner-docker/engine/compiler"
	"github.com/drone-runners/drone-runner-docker/engine/linter"
	"github.com/drone-runners/drone-runner-docker/engine/resource"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/kameshsampath/drone-provenance/pkg/utils"

	"github.com/drone/drone-go/drone"
	"github.com/drone/envsubst"
	"github.com/drone/runner-go/environ"
	"github.com/drone/runner-go/environ/provider"
	"github.com/drone/runner-go/labels"
	"github.com/drone/runner-go/logger"
	"github.com/drone/runner-go/manifest"
	"github.com/drone/runner-go/pipeline"
	"github.com/drone/runner-go/pipeline/runtime"
	"github.com/drone/runner-go/pipeline/streamer/console"
	"github.com/drone/runner-go/registry"
	"github.com/drone/runner-go/secret"
	"github.com/drone/signal"

	"github.com/sirupsen/logrus"
	"github.com/urfave/cli/v2"

	intoto "github.com/in-toto/in-toto-golang/in_toto"
	"github.com/in-toto/in-toto-golang/in_toto/slsa_provenance/common"
	slsa "github.com/in-toto/in-toto-golang/in_toto/slsa_provenance/v0.2"
)

const (
	darwinExtensionSocketPath = "Library/Containers/com.docker.docker/Data/ext-sockets/drone_drone-ci-docker-extension/extension-drone-ci.sock"
	// labelPipelineFile is to identify the pipeline file
	labelPipelineFile = "io.drone.desktop.pipeline.file"
	// labelIncludes is to hold list of included steps as comma separated string
	labelIncludes = "io.drone.desktop.pipeline.includes"
	// labelExcludes is to hold list of excluded steps as comma separated string
	labelExcludes = "io.drone.desktop.pipeline.excludes"
	// labelStageName is to identify the stage name
	labelStageName = "io.drone.stage.name"
	// labelStepName is to identify the step name
	labelStepName = "io.drone.step.name"
	// labelStepNumber is to identify the step number
	labelStepNumber = "io.drone.step.number"
	// labelService to identify if the step is a "Service"
	labelService = "io.drone.desktop.pipeline.service"
)

var (
	nocontext      = context.Background()
	log            = utils.LogSetup(os.Stdout, "info")
	droneCIHome    string
	droneCILogsDir string
	dockerCli      *client.Client
)

// Command exports the exec command.
var Command = &cli.Command{
	Name:      "exec",
	Usage:     "execute a local build",
	ArgsUsage: "[path/to/.drone.yml]",
	Before: func(ctx *cli.Context) error {
		return nil
	},
	Action: func(ctx *cli.Context) error {
		if err := exec(ctx); err != nil {
			log.Fatalln(err)
		}
		return nil
	},
	Flags: []cli.Flag{
		&cli.StringFlag{
			Name:  "pipeline",
			Usage: "Name of the pipeline to execute",
		},
		&cli.StringSliceFlag{
			Name:  "include",
			Usage: "Name of steps to include",
		},
		&cli.StringSliceFlag{
			Name:  "exclude",
			Usage: "Name of steps to exclude",
		},
		&cli.StringFlag{
			Name:  "resume-at",
			Usage: "Name of start to resume at",
		},
		&cli.BoolFlag{
			Name:  "trusted",
			Usage: "build is trusted",
		},
		&cli.DurationFlag{
			Name:  "timeout",
			Usage: "build timeout",
			Value: time.Hour,
		},
		&cli.StringSliceFlag{
			Name:  "volume",
			Usage: "build volumes",
		},
		&cli.StringSliceFlag{
			Name:  "network",
			Usage: "external networks",
		},
		&cli.StringFlag{
			Name:  "registry",
			Usage: "registry file",
		},
		&cli.StringFlag{
			Name:    "secret-file",
			Aliases: []string{"secrets"},
			Usage:   "secret file, define values that can be used with from_secret",
		},
		&cli.StringFlag{
			Name:  "env-file",
			Usage: "env file",
		},
		&cli.StringSliceFlag{
			Name:  "privileged",
			Usage: "privileged plugins",
			Value: cli.NewStringSlice(
				"plugins/docker",
				"plugins/acr",
				"plugins/ecr",
				"plugins/gcr",
				"plugins/heroku",
			),
		},
	},
}

func exec(cliContext *cli.Context) error {
	var err error
	dockerCli, err = utils.DockerCliClient()
	if err != nil {
		return err
	}
	// lets do our mapping from CLI flags to an execCommand struct
	commy := toExecCommand(cliContext)
	rawsource, err := ioutil.ReadFile(commy.Source)
	if err != nil {
		return err
	}
	envs := environ.Combine(
		getEnv(cliContext),
		environ.System(commy.System),
		environ.Repo(commy.Repo),
		environ.Build(commy.Build),
		environ.Stage(commy.Stage),
		environ.Link(commy.Repo, commy.Build, commy.System),
		commy.Build.Params,
	)

	// string substitution function ensures that string
	// replacement variables are escaped and quoted if they
	// contain newlines.
	subf := func(k string) string {
		v := envs[k]
		if strings.Contains(v, "\n") {
			v = fmt.Sprintf("%q", v)
		}
		return v
	}

	// evaluates string replacement expressions and returns an
	// update configuration.
	config, err := envsubst.Eval(string(rawsource), subf)
	if err != nil {
		return err
	}

	// parse and lint the configuration.
	manifest, err := manifest.ParseString(config)
	if err != nil {
		return err
	}

	// a configuration can contain multiple pipelines.
	// get a specific pipeline resource for execution.
	if commy.Stage.Name == "" {
		log.Infoln("No stage specified, assuming 'default'")
		commy.Stage.Name = "default"
	}

	res, err := resource.Lookup(commy.Stage.Name, manifest)
	if err != nil {
		return fmt.Errorf("stage '%s' not found in build file : %w", commy.Stage.Name, err)
	}

	// lint the pipeline and return an error if any
	// linting rules are broken
	lint := linter.New()
	err = lint.Lint(res, commy.Repo)
	if err != nil {
		return err
	}

	// compile the pipeline to an intermediate representation.
	comp := &compiler.Compiler{
		Environ:    provider.Static(commy.Environ),
		Labels:     commy.Labels,
		Resources:  commy.Resources,
		Tmate:      commy.Tmate,
		Privileged: append(commy.Privileged, compiler.Privileged...),
		Networks:   commy.Networks,
		Volumes:    commy.Volumes,
		Secret:     secret.StaticVars(commy.Secrets),
		Registry: registry.Combine(
			registry.File(commy.Config),
		),
	}

	// when running a build locally cloning is always
	// disabled in favor of mounting the source code
	// from the current working directory.
	if !commy.Clone {
		pwd, _ := os.Getwd()
		comp.Mount = pwd
		//Add the new labels that helps looking up the step containers
		//by names
		if comp.Labels == nil {
			comp.Labels = make(map[string]string)
		}
		comp.Labels[labelPipelineFile] = path.Join(pwd, commy.Source)
	}

	args := runtime.CompilerArgs{
		Pipeline: res,
		Manifest: manifest,
		Build:    commy.Build,
		Netrc:    commy.Netrc,
		Repo:     commy.Repo,
		Stage:    commy.Stage,
		System:   commy.System,
		Secret:   secret.StaticVars(commy.Secrets),
	}
	spec := comp.Compile(nocontext, args).(*engine.Spec)

	//Handle to parsed Pipeline
	p := res.(*resource.Pipeline)

	//As the Compiler does not add labels for Steps adding few here
	for i, step := range spec.Steps {
		extraLabels := map[string]string{}

		extraLabels[labelStageName] = strings.TrimSpace(p.Name)
		extraLabels[labelStepName] = strings.TrimSpace(step.Name)
		extraLabels[labelStepNumber] = strconv.Itoa(i)

		//Know the includes while running the pipeline from the extension
		//TODO improve
		if len(commy.Include) > 0 {
			extraLabels[labelIncludes] = strings.Join(commy.Include, ",")
		}

		//Know the excludes while running the pipeline from the extension
		if len(commy.Exclude) > 0 {
			extraLabels[labelExcludes] = strings.Join(commy.Exclude, ",")
		}
		//Label the services from steps
		for _, svc := range p.Services {
			if b := step.Name == svc.Name; b {
				log.Tracef("%s Service == Step %s", svc.Name, step.Name)
				extraLabels[labelService] = strconv.FormatBool(b)
				break
			}
		}
		step.Labels = labels.Combine(step.Labels, extraLabels)

		log.Tracef("Step %s, Labels: %#v", step.Name, step.Labels)
	}

	// include only steps that are in the include list,
	// if the list in non-empty.
	if len(commy.Include) > 0 {
	I:
		for _, step := range spec.Steps {
			if step.Name == "clone" {
				continue
			}
			for _, name := range commy.Include {
				if step.Name == name {
					continue I
				}
			}
			step.RunPolicy = runtime.RunNever
		}
	}
	// exclude steps that are in the exclude list, if the list in non-empty.
	if len(commy.Exclude) > 0 {
	E:
		for _, step := range spec.Steps {
			if step.Name == "clone" {
				continue
			}
			for _, name := range commy.Exclude {
				if step.Name == name {
					step.RunPolicy = runtime.RunNever
					continue E
				}
			}
		}
	}
	// resume at a specific step
	if cliContext.String("resume-at") != "" {
		for _, step := range spec.Steps {
			if step.Name == cliContext.String("resume-at") {
				break
			}
			if step.Name == "clone" {
				continue
			}
			for _, name := range commy.Exclude {
				if step.Name == name {
					step.RunPolicy = runtime.RunNever
					continue
				}
			}
		}
	}
	// create a step object for each pipeline step.
	for _, step := range spec.Steps {
		if step.RunPolicy == runtime.RunNever {
			continue
		}

		commy.Stage.Steps = append(commy.Stage.Steps, &drone.Step{
			StageID:   commy.Stage.ID,
			Number:    len(commy.Stage.Steps) + 1,
			Name:      step.Name,
			Status:    drone.StatusPending,
			ErrIgnore: step.ErrPolicy == runtime.ErrIgnore,
		})
	}

	// configures the pipeline timeout.
	timeout := time.Duration(commy.Repo.Timeout) * time.Minute
	ctx, cancel := context.WithTimeout(nocontext, timeout)
	defer cancel()

	// listen for operating system signals and cancel execution when received.
	ctx = signal.WithContextFunc(ctx, func() {
		println("received signal, terminating process")
		cancel()
	})

	state := &pipeline.State{
		Build:  commy.Build,
		Stage:  commy.Stage,
		Repo:   commy.Repo,
		System: commy.System,
	}

	// enable debug logging
	if commy.Debug {
		log.SetLevel(logrus.DebugLevel)
	}
	if commy.Trace {
		log.SetLevel(logrus.TraceLevel)
	}
	logger.Default = logger.Logrus(
		logrus.NewEntry(
			log,
		),
	)

	engine, err := engine.NewEnv(engine.Opts{})
	if err != nil {
		return err
	}

	err = runtime.NewExecer(
		pipeline.NopReporter(),
		console.New(commy.Pretty),
		pipeline.NopUploader(),
		engine,
		commy.Procs,
	).Exec(ctx, spec, state)

	if err != nil {
		dump(state)
		return err
	}

	switch state.Stage.Status {
	case drone.StatusError, drone.StatusFailing, drone.StatusKilled:
		os.Exit(1)
	}

	if err != nil {
		return err
	}

	generateStatement(commy, p, spec)

	return nil
}

func dump(v interface{}) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func generateStatement(commy *execCommand, p *resource.Pipeline, spec *engine.Spec) {
	//TODO
	var subjects []intoto.Subject
	att := intoto.ProvenanceStatement{
		StatementHeader: intoto.StatementHeader{
			Type:          intoto.StatementInTotoV01,
			PredicateType: slsa.PredicateSLSAProvenance,
			Subject:       subjects,
		},
		Predicate: slsa.ProvenancePredicate{
			BuildType: p.Kind + "/" + p.Type,
			Builder: common.ProvenanceBuilder{
				ID: "https://harness.drone.io/Attestations/DockerRunner",
			},
			Metadata: &slsa.ProvenanceMetadata{
				BuildInvocationID: fmt.Sprintf("%d", commy.Build.ID),
			},
			Invocation: slsa.ProvenanceInvocation{
				Parameters: commy.Build.Params,
			},
			BuildConfig: map[string][]*engine.Step{
				"steps": spec.Steps,
			},
			Materials: materials(spec),
		},
	}

	pf := commy.Source
	//TODO: save/upload to storage/repo for now dump json to file
	fp := path.Join(path.Dir(pf), path.Base(pf), "-provenance.json")
	f, err := os.Create(fp)
	if err != nil {
		log.Errorf("Error generating attestation,%v", err)
		return
	}
	enc := json.NewEncoder(f)
	if err := enc.Encode(att); err != nil {
		log.Errorf("Error generating attestation json,%v", err)
	}
}

func buildConfig(spec *engine.Spec) map[string]string {
	bc := make(map[string]string)

	return bc
}

func materials(spec *engine.Spec) []common.ProvenanceMaterial {
	var mat []common.ProvenanceMaterial
	for _, s := range spec.Steps {
		dig, _ := crane.Digest(s.Image)
		mat = append(mat, common.ProvenanceMaterial{
			URI: fmt.Sprintf("pkg:%s@%s", s.Image, dig),
			Digest: common.DigestSet{
				"sha256": dig,
			},
		})
	}
	return mat
}
