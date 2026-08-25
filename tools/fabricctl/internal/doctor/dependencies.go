package doctor

import (
	"context"
	"net/http"
	"os/exec"
	"runtime"
)

type CommandOutput struct {
	Stdout string
	Stderr string
}

type CommandRunner interface {
	LookPath(file string) (string, error)
	Run(ctx context.Context, name string, args ...string) (CommandOutput, error)
}

type platform interface {
	OS() string
	Arch() string
}

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type Dependencies struct {
	Commands CommandRunner
	Platform platform
	HTTP     HTTPClient
}

type systemCommands struct{}

func (systemCommands) LookPath(file string) (string, error) { return exec.LookPath(file) }

func (systemCommands) Run(ctx context.Context, name string, args ...string) (CommandOutput, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.Output()
	result := CommandOutput{Stdout: string(out)}
	if exitErr, ok := err.(*exec.ExitError); ok {
		result.Stderr = string(exitErr.Stderr)
	}
	return result, err
}

type systemPlatform struct{}

func (systemPlatform) OS() string   { return runtime.GOOS }
func (systemPlatform) Arch() string { return runtime.GOARCH }

func SystemDependencies() Dependencies {
	return Dependencies{
		Commands: systemCommands{},
		Platform: systemPlatform{},
		HTTP: &http.Client{
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}
