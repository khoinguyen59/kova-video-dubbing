# KOVA Wails desktop blank-window fix

## Symptom

`KOVA-Desktop-Wails-current.exe`, made by invoking Go directly, could create a
native KOVA window whose WebView showed only the dark background.  A preceding
build made without Wails production assets could also show Wails' build-tag
dialog.

## Cause

The desktop executable was built outside the Wails packaging workflow.  That
workflow is responsible for generating the Go-to-React bindings, compiling the
Vite bundle, and packaging the Windows WebView2 loader with the application.
The Wails 2.9 CLI bundled previously also could not generate bindings under the
project's current Go toolchain.

## Correct build

Use a compatible Wails CLI (validated: `v2.13.0`) and run:

```powershell
wails build -clean -nopackage -webview2 browser -o KOVA-Desktop-Wails.exe
```

The output is `build\\bin\\KOVA-Desktop-Wails.exe`.  Do not substitute a plain
`go build .` command for this application target.

## Verification performed

- Wails build completed all four required steps: bindings, frontend install,
  frontend compilation, and Windows application compilation.
- React/TypeScript typecheck passed with `npm run typecheck`.
- Go package tests passed with `go test .`.
- The generated executable contains the packaged KOVA React frontend and is
  separate from the older executable, so it cannot be confused with a file
  already opened by Windows.

The final visual launch remains a user acceptance check on the target Windows
machine, because WebView2 is supplied by that machine rather than emulated by
the build process.
