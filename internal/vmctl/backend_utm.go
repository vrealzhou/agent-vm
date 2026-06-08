package vmctl

type UTMBackend struct{}

func (u *UTMBackend) Start(cfg Config) error {
	return Start(cfg)
}

func (u *UTMBackend) Stop(cfg Config) error {
	return Stop(cfg)
}

func (u *UTMBackend) Destroy(cfg Config) error {
	return Destroy(cfg)
}

func (u *UTMBackend) IsRunning(cfg Config) (bool, error) {
	return utmVMIsRunning(cfg.Name)
}

func (u *UTMBackend) Status(cfg Config) (VMStatus, error) {
	return InspectVM(cfg)
}

func (u *UTMBackend) Exec(cfg Config, args ...string) error {
	return SSH(cfg, args)
}

func (u *UTMBackend) BootstrapSetup(cfg Config) error {
	return BootstrapSetup(cfg)
}
