package events

import "github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"

func ObjectFromMap(input map[string]any) extension.Object {
	if len(input) == 0 {
		return extension.Object{}
	}
	value, err := extension.FromJSON(input)
	if err != nil {
		return extension.Object{}
	}
	out, ok := value.ObjectValue()
	if !ok {
		return extension.Object{}
	}
	return out
}

func objectFromMap(input map[string]any) extension.Object {
	return ObjectFromMap(input)
}
