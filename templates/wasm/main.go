//go:build js && wasm

package main

import (
	"syscall/js"
	"time"
)

func main() {
	js.Global().Set("__WASM_GLOBAL_METADATA", js.Global().Get("Object").New())
	js.Global().Set("wasmReady", js.ValueOf(true))
	js.Global().Set("wasmVersion", js.ValueOf("{{FOUNDATION_VERSION}}"))
	js.Global().Set("sendWasmMessage", js.FuncOf(jsSendMessage))
	js.Global().Set("connectWebSocket", js.FuncOf(jsConnectWebSocket))
	js.Global().Set("disconnectWebSocket", js.FuncOf(jsDisconnectWebSocket))
	js.Global().Set("setFrontendReady", js.FuncOf(jsSetFrontendReady))
	js.Global().Set("emitWasmCompatMessage", js.FuncOf(jsEmitCompatMessage))

	wasmLog("initialized as runtime-transport compatibility shim")
	select {}
}

func jsSendMessage(this js.Value, args []js.Value) any {
	if len(args) == 0 {
		return nil
	}
	dispatch := runtimeTransportDispatch()
	if dispatch.Type() != js.TypeFunction {
		wasmLog("runtime transport dispatch is unavailable")
		return nil
	}
	dispatch.Invoke(args[0])
	return nil
}

func jsConnectWebSocket(this js.Value, args []js.Value) any {
	updateMetadata("webSocketManagedByRuntimeTransport", true)
	notifyFrontend("connection:status", map[string]any{
		"connected": false,
		"reason":    "runtime_transport_owns_websocket",
	})
	return nil
}

func jsDisconnectWebSocket(this js.Value, args []js.Value) any {
	updateMetadata("webSocketManagedByRuntimeTransport", true)
	return nil
}

func jsSetFrontendReady(this js.Value, args []js.Value) any {
	updateMetadata("frontendReady", true)
	return nil
}

func jsEmitCompatMessage(this js.Value, args []js.Value) any {
	if len(args) == 0 {
		return nil
	}
	handler := js.Global().Get("onWasmMessage")
	if handler.Type() == js.TypeFunction {
		handler.Invoke(args[0])
	}
	return nil
}

func runtimeTransportDispatch() js.Value {
	transport := js.Global().Get("__OVASABI_RUNTIME_TRANSPORT")
	if transport.IsNull() || transport.IsUndefined() {
		return js.Undefined()
	}
	return transport.Get("dispatch")
}

func updateMetadata(key string, value any) {
	metadata := js.Global().Get("__WASM_GLOBAL_METADATA")
	if metadata.Truthy() {
		metadata.Set(key, js.ValueOf(value))
	}
}

func notifyFrontend(eventType string, payload map[string]any) {
	handler := js.Global().Get("onWasmMessage")
	if handler.Type() != js.TypeFunction {
		return
	}
	eventObj := js.Global().Get("Object").New()
	eventObj.Set("type", eventType)
	eventObj.Set("event_type", eventType)
	eventObj.Set("payload", toJSValue(payload))
	eventObj.Set("timestamp", time.Now().UTC().Format(time.RFC3339))
	handler.Invoke(eventObj)
}

func toJSValue(value any) js.Value {
	switch typed := value.(type) {
	case string:
		return js.ValueOf(typed)
	case int:
		return js.ValueOf(typed)
	case float64:
		return js.ValueOf(typed)
	case bool:
		return js.ValueOf(typed)
	case map[string]any:
		obj := js.Global().Get("Object").New()
		for key, item := range typed {
			obj.Set(key, toJSValue(item))
		}
		return obj
	case []any:
		arr := js.Global().Get("Array").New(len(typed))
		for i, item := range typed {
			arr.SetIndex(i, toJSValue(item))
		}
		return arr
	default:
		return js.Null()
	}
}

func wasmLog(message string) {
	js.Global().Get("console").Call("log", "[{{PROJECT_NAME}} wasm]", message)
}
