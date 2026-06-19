package secrets

// Field defines a single credential field's visibility.
type Field struct {
	Secret   bool // true = proxy-only, false = can enter container as env var
	Optional bool // true = field may be omitted
}

// Type defines a credential type with its field visibility rules.
type Type struct {
	Name   string
	Fields map[string]Field
}

// BuiltinTypes are the predefined credential types.
var BuiltinTypes = map[string]Type{
	"aws-sigv4": {
		Name: "aws-sigv4",
		Fields: map[string]Field{
			"access_key":    {Secret: false},
			"secret_key":    {Secret: true},
			"session_token": {Secret: true, Optional: true},
			"region":        {Secret: false},
			"service":       {Secret: false},
		},
	},
	"header": {
		Name: "header",
		Fields: map[string]Field{
			// headers is a special field: map[string]string stored as YAML
			// All header values are secret
		},
	},
	"kafka-sasl": {
		Name: "kafka-sasl",
		Fields: map[string]Field{
			"broker":        {Secret: false},
			"sasl_username": {Secret: false},
			"sasl_password": {Secret: true},
			"tls":           {Secret: false},
		},
	},
}

// GetType returns the Type definition for a credential type name.
// Returns nil if the type is unknown.
func GetType(typeName string) *Type {
	t, ok := BuiltinTypes[typeName]
	if !ok {
		return nil
	}
	return &t
}

// IsSecretField returns true if the field is secret for the given type.
func IsSecretField(typeName, fieldName string) bool {
	t := GetType(typeName)
	if t == nil {
		return true // unknown type: treat all as secret
	}
	f, ok := t.Fields[fieldName]
	if !ok {
		return true // unknown field: treat as secret
	}
	return f.Secret
}

// PlaintextFields returns field names that are safe to expose to the container.
func PlaintextFields(typeName string) []string {
	t := GetType(typeName)
	if t == nil {
		return nil
	}
	var result []string
	for name, f := range t.Fields {
		if !f.Secret {
			result = append(result, name)
		}
	}
	return result
}

// SecretFields returns field names that are proxy-only.
func SecretFields(typeName string) []string {
	t := GetType(typeName)
	if t == nil {
		return nil
	}
	var result []string
	for name, f := range t.Fields {
		if f.Secret {
			result = append(result, name)
		}
	}
	return result
}

// TypeNames returns all registered type names.
func TypeNames() []string {
	var names []string
	for name := range BuiltinTypes {
		names = append(names, name)
	}
	return names
}
