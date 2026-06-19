package secrets

import (
	"os"

	"gopkg.in/yaml.v3"

	"github.com/vrealzhou/agent-vm/internal/config"
)

// Placeholder is a named credential entry with a type and fields.
type Placeholder struct {
	Type   string            `yaml:"type"`
	Fields map[string]string `yaml:"fields"`
}

// Store is the placeholder store, backed by secrets.yaml.
type Store struct {
	Placeholders map[string]Placeholder `yaml:"placeholders"`
}

// Load reads the global secrets.yaml.
func Load() (*Store, error) {
	data, err := os.ReadFile(config.SecretsConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return &Store{Placeholders: make(map[string]Placeholder)}, nil
		}
		return nil, err
	}
	var s Store
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	if s.Placeholders == nil {
		s.Placeholders = make(map[string]Placeholder)
	}
	return &s, nil
}

// Save writes the store to secrets.yaml.
func (s *Store) Save() error {
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(config.SecretsConfigPath(), data, 0o600)
}

// Add inserts or updates a placeholder.
func (s *Store) Add(name string, p Placeholder) {
	if s.Placeholders == nil {
		s.Placeholders = make(map[string]Placeholder)
	}
	s.Placeholders[name] = p
}

// Get returns a placeholder by name.
func (s *Store) Get(name string) (Placeholder, bool) {
	p, ok := s.Placeholders[name]
	return p, ok
}

// Remove deletes a placeholder by name. Returns false if not found.
func (s *Store) Remove(name string) bool {
	if _, ok := s.Placeholders[name]; !ok {
		return false
	}
	delete(s.Placeholders, name)
	return true
}

// List returns all placeholder names.
func (s *Store) List() []string {
	var names []string
	for name := range s.Placeholders {
		names = append(names, name)
	}
	return names
}

// MaskedFields returns field values with secret fields replaced by "****".
func MaskedFields(p Placeholder) map[string]string {
	result := make(map[string]string)
	for k, v := range p.Fields {
		if IsSecretField(p.Type, k) {
			result[k] = "****"
		} else {
			result[k] = v
		}
	}
	return result
}
