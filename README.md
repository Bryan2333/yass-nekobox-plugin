# Yass Plugin for NekoBox (NaiveProxy Wrapper)

This is a standalone Android plugin for [NekoBoxForAndroid](https://github.com/MatsuriDayo/NekoBoxForAndroid) that allows running the `yass_cli` core by masquerading as a NaiveProxy plugin.

## Project Structure

- `main.go`: The Go wrapper source code. It translates NaiveProxy JSON config to Yass format and prevents routing loops.
- `app/`: The Android project for the plugin APK.
- `integration_guide.md`: Detailed technical explanation of the implementation.

## How to Build

### 1. Build the Go Wrapper
You need the Go SDK installed.
```bash
export GOOS=android
export GOARCH=arm64
export CGO_ENABLED=0
go build -ldflags="-s -w" -trimpath -o app/libs/arm64-v8a/libnaive.so main.go
```

### 2. Add yass_cli
Place your compiled `yass_cli` binary (Android arm64) into `app/libs/arm64-v8a/` and rename it to `libyass_cli.so`.

### 3. Build the APK
```bash
export APK_VERSION_NAME="v1.0.0-1"
export APK_ABI="arm64-v8a"
export KEYSTORE_PASS="your_password"
./gradlew assembleRelease
```

## Thanks to
- [hukeyue/yass](https://github.com/hukeyue/yass/issues)
- [klzgrad/naiveproxy](https://github.com/klzgrad/naiveproxy)
- [MatsuriDayo/NekoBoxForAndroid](https://github.com/MatsuriDayo/NekoBoxForAndroid)
