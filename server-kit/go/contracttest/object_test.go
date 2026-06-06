package contracttest

import "github.com/nmxmxh/ovasabi_foundation/server-kit/go/extension"

func contractObject(values map[string]any) extension.Object {
	value, err := extension.FromJSON(values)
	if err != nil {
		panic(err)
	}
	object, ok := value.ObjectValue()
	if !ok {
		panic("contract test value is not object")
	}
	return object
}
