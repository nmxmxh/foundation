# Capability Examples

The active native shell uses only `main.json` by default. Keep that list narrow.
When a project adds a Tauri plugin, copy the relevant snippet into a new
capability file and add the new capability identifier to
`app.security.capabilities` in `tauri.conf.json`, `tauri.dev.conf.json`, and
`tauri.prod.conf.json`.

Filesystem read example:

```json
{
  "$schema": "../gen/schemas/desktop-schema.json",
  "identifier": "filesystem-read",
  "description": "Read project-selected files only.",
  "windows": ["main"],
  "permissions": [
    "fs:allow-read-file"
  ]
}
```

Notifications example:

```json
{
  "$schema": "../gen/schemas/desktop-schema.json",
  "identifier": "notifications",
  "description": "Allow user-visible notifications.",
  "windows": ["main"],
  "permissions": [
    "notification:default"
  ]
}
```

Shell sidecar example:

```json
{
  "$schema": "../gen/schemas/desktop-schema.json",
  "identifier": "shell-sidecar",
  "description": "Allow a named sidecar only; do not grant broad shell access.",
  "windows": ["main"],
  "permissions": [
    "shell:allow-spawn"
  ]
}
```

Device plugin notes:

- Add the Tauri plugin crate/package before enabling the capability.
- Add iOS privacy strings and Android manifest permissions/features in the same
  change.
- Keep remote origins unprivileged.
- Use plugin commands for low-rate control. Use Foundation binary/native lanes
  for raw camera, microphone, and sensor streams.

Geolocation example:

```json
{
  "$schema": "../gen/schemas/desktop-schema.json",
  "identifier": "geolocation",
  "description": "Allow bounded device location requests for app-owned flows.",
  "windows": ["main"],
  "permissions": [
    "geolocation:default"
  ]
}
```

Biometric auth example:

```json
{
  "$schema": "../gen/schemas/desktop-schema.json",
  "identifier": "biometric",
  "description": "Allow native biometric verification as an auth assertion.",
  "windows": ["main"],
  "permissions": [
    "biometric:default"
  ]
}
```

Barcode scanner example:

```json
{
  "$schema": "../gen/schemas/desktop-schema.json",
  "identifier": "barcode-scanner",
  "description": "Allow native barcode scanning for user-initiated scan flows.",
  "windows": ["main"],
  "permissions": [
    "barcode-scanner:default"
  ]
}
```

NFC example:

```json
{
  "$schema": "../gen/schemas/desktop-schema.json",
  "identifier": "nfc",
  "description": "Allow user-initiated NFC reads.",
  "windows": ["main"],
  "permissions": [
    "nfc:default"
  ]
}
```

Haptics example:

```json
{
  "$schema": "../gen/schemas/desktop-schema.json",
  "identifier": "haptics",
  "description": "Allow user-feedback haptic events only.",
  "windows": ["main"],
  "permissions": [
    "haptics:default"
  ]
}
```

Raw camera, microphone, and sensor examples should not start as broad generic
capabilities. Define product-specific plugin commands, permission names, stream
schemas, and frame budgets first; then expose only those commands to the main
window capability.
