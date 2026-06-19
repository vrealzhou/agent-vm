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
		Name:   "header",
		Fields: map[string]Field{},
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
		return true
	}
	f, ok := t.Fields[fieldName]
	if !ok {
		return true
	}
	return f.Secret
}
