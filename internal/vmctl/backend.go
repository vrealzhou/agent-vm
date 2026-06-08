package vmctl

type Backend interface {
	Start(cfg Config) error
	Stop(cfg Config) error
	Destroy(cfg Config) error
	IsRunning(cfg Config) (bool, error)
	Status(cfg Config) (VMStatus, error)
	Exec(cfg Config, args ...string) error
	BootstrapSetup(cfg Config) error
}

func NewBackend(cfg Config) Backend {
	switch cfg.Backend {
	case "sbx":
		return &SBXBackend{}
	case "podman":
		return &PodmanBackend{}
	default:
		return &UTMBackend{}
	}
}
