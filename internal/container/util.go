package container

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
)

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func runCommand(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func runWithSignals(cmd *exec.Cmd) error {
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return err
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigCh)

	go func() {
		for sig := range sigCh {
			if cmd.Process != nil {
				_ = cmd.Process.Signal(sig)
			}
		}
	}()

	return cmd.Wait()
}

func checkContainerCLI() error {
	if _, err := exec.LookPath("container"); err != nil {
		return fmt.Errorf("Apple Container CLI not found (requires macOS 26+)")
	}
	return nil
}
