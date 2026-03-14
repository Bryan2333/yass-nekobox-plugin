# Replacing NaiveProxy with Yass in NekoBoxForAndroid

This guide documents the process of integrating a custom proxy core (`yass_cli`) into NekoBoxForAndroid by disguising it as the NaiveProxy plugin, without modifying the NekoBox application source code.

## 1. The Challenge: Plugin Architecture & Routing Loops

NekoBox uses a plugin architecture where it extracts native libraries (`.so` files) from a plugin APK and executes them as background child processes. When running a plugin, NekoBox generates a JSON configuration file specific to that plugin (e.g., `naive_config.json`) and passes the file path as a command-line argument.

To replace NaiveProxy with `yass_cli`, we face three main challenges:
1.  **Configuration Translation:** NekoBox outputs NaiveProxy-formatted JSON, but `yass_cli` requires a different JSON schema.
2.  **Process Management:** NekoBox tracks the lifecycle of the plugin process. We need a way to wrap the execution without breaking signal handling (e.g., stopping the proxy).
3.  **VPN Routing Loop (TUN Loop):** If a standard CLI tool on Android tries to connect to a remote server while the VPN (VpnService) is active, its outbound traffic is captured by the VPN and routed back into the tunnel, causing an infinite loop. NekoBox prevents this by instructing plugins (via `host-resolver-rules`) to connect to a local bypass port (`127.0.0.1:xxx`) instead of the real remote IP. The wrapper must respect this mapping.

## 2. The Solution: A Go Wrapper (`libnaive.so`)

Instead of modifying the `yass` source code, we use a lightweight Go wrapper. This wrapper intercepts the NekoBox execution command, translates the NaiveProxy configuration on the fly, and then uses `syscall.Exec` to replace itself with the actual `yass_cli` binary.

### Wrapper Source Code (`main.go`)

```go
package main

import (
	"encoding/json"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type NaiveConfig struct {
	Listen              string `json:"listen"`
	Proxy               string `json:"proxy"`
	InsecureConcurrency int    `json:"insecure-concurrency,omitempty"`
	HostResolverRules   string `json:"host-resolver-rules,omitempty"` // Crucial for preventing routing loops
}

type YassConfig struct {
	Local                  string `json:"local"`
	LocalPort              int    `json:"local_port"`
	Method                 string `json:"method"`
	Server                 string `json:"server"`
	ServerSni              string `json:"server_sni"`
	ServerPort             int    `json:"server_port"`
	ConnectTimeout         int    `json:"connect_timeout"`
	Username               string `json:"username"`
	Password               string `json:"password"`
	InsecureMode           bool   `json:"insecure_mode"`
	CertificateChainFile   string `json:"certificate_chain_file"`
	EnablePostQuantumKyber bool   `json:"enable_post_quantum_kyber"`
	TcpCongestionAlgorithm string `json:"tcp_congestion_algorithm"`
}

func main() {
	var configPath string
	if len(os.Args) > 1 {
		configPath = os.Args[len(os.Args)-1]
	}

	if configPath == "" || !strings.HasSuffix(configPath, ".json") {
		log.Fatalf("Error: No JSON config path found. Args: %v", os.Args)
	}

	configBytes, err := os.ReadFile(configPath)
	if err != nil {
		log.Fatalf("Read naive config error: %v", err)
	}

	var naive NaiveConfig
	if err := json.Unmarshal(configBytes, &naive); err != nil {
		log.Fatalf("Parse naive JSON error: %v", err)
	}

	listenURL, err := url.Parse(naive.Listen)
	if err != nil {
		log.Fatalf("Parse Listen URL error: %v", err)
	}
	localPort, _ := strconv.Atoi(listenURL.Port())
	localIP := listenURL.Hostname()

	proxyURL, err := url.Parse(naive.Proxy)
	if err != nil {
		log.Fatalf("Parse Proxy URL error: %v", err)
	}
	serverPort, _ := strconv.Atoi(proxyURL.Port())
	if serverPort == 0 {
		if proxyURL.Scheme == "https" {
			serverPort = 443
		} else {
			serverPort = 80
		}
	}
	username := proxyURL.User.Username()
	password, _ := proxyURL.User.Password()

	// [CRITICAL] Prevent Routing Loop by mapping to NekoBox's local bypass port
	serverAddr := proxyURL.Hostname()
	if naive.HostResolverRules != "" {
		// NekoBox format: "MAP example.com 127.0.0.1"
		parts := strings.Split(naive.HostResolverRules, " ")
		if len(parts) >= 3 && parts[0] == "MAP" {
			serverAddr = parts[2]
		}
	}

	yass := YassConfig{
		Local:                  localIP,
		LocalPort:              localPort,
		Method:                 "http2",
		Server:                 serverAddr,          // Connect to mapped local port (127.0.0.1)
		ServerSni:              proxyURL.Hostname(), // Keep original domain for SNI
		ServerPort:             serverPort,
		ConnectTimeout:         2000,
		Username:               username,
		Password:               password,
		InsecureMode:           false,
		CertificateChainFile:   "",
		EnablePostQuantumKyber: false,
		TcpCongestionAlgorithm: "",
	}

	yassConfigBytes, err := json.MarshalIndent(yass, "", "    ")
	if err != nil {
		log.Fatalf("Marshal Yass config error: %v", err)
	}

	yassConfigPath := filepath.Join(filepath.Dir(configPath), "yass_generated.json")
	if err := os.WriteFile(yassConfigPath, yassConfigBytes, 0600); err != nil {
		log.Fatalf("Write Yass config error: %v", err)
	}

	execPath, err := os.Executable()
	if err != nil {
		log.Fatalf("Get executable path error: %v", err)
	}
	yassBinaryPath := filepath.Join(filepath.Dir(execPath), "libyass_cli.so")

	if _, err := os.Stat(yassBinaryPath); os.IsNotExist(err) {
		log.Fatalf("Yass core not found at: %s", yassBinaryPath)
	}

	// Replace the wrapper process with yass_cli using the correct `-K` flag
	yassArgs := []string{"libyass_cli.so", "-K", yassConfigPath}
	if err := syscall.Exec(yassBinaryPath, yassArgs, os.Environ()); err != nil {
		log.Fatalf("Exec yass error: %v", err)
	}
}
```

### Compiling the Wrapper
Compile the Go code targeting Android `arm64-v8a` as a standalone binary named `libnaive.so`:
```bash
GOOS=android GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -trimpath -o libnaive.so main.go
```

## 3. Packaging the APK Plugin

To deploy this to Android, we utilize the existing NaiveProxy plugin APK project (`naiveproxy/apk`), modifying it to extract both our wrapper and the actual `yass` binary.

### 3.1. Place the Binaries
Create an ABI-specific directory in the Android project and place both the wrapper and the real binary there:
```text
app/libs/
    └── arm64-v8a/
        ├── libnaive.so       <-- The compiled Go Wrapper
        └── libyass_cli.so    <-- The compiled Yass core binary
```

### 3.2. Modify the Content Provider
By default, the APK only extracts the `libnaive.so` file. We must instruct the `BinaryProvider` to also extract `libyass_cli.so`.

Modify `app/src/main/java/io/nekohasekai/sagernet/plugin/naive/BinaryProvider.kt`:

```kotlin
class BinaryProvider : NativePluginProvider() {
    override fun populateFiles(provider: PathProvider) {
        provider.addPath("naive-plugin", 0b111101101)
        provider.addPath("libyass_cli.so", 0b111101101) // Ensure yass_cli is extracted
    }

    override fun getExecutable() = context!!.applicationInfo.nativeLibraryDir + "/libnaive.so"
    
    override fun openFile(uri: Uri): ParcelFileDescriptor = when (uri.path) {
        "/naive-plugin" -> ParcelFileDescriptor.open(
            File(getExecutable()),
            ParcelFileDescriptor.MODE_READ_ONLY
        )
        "/libyass_cli.so" -> ParcelFileDescriptor.open(
            File(context!!.applicationInfo.nativeLibraryDir + "/libyass_cli.so"),
            ParcelFileDescriptor.MODE_READ_ONLY
        )
        else -> throw FileNotFoundException()
    }
}
```

### 3.3. Create a Signing Keystore
If you do not have the original `release.keystore` password, generate a new one:
```bash
cd naiveproxy/apk
keytool -genkey -v -keystore release.keystore -alias release -keyalg RSA -keysize 2048 -validity 10000
# Set password to '123456' for simplicity
```

### 3.4. Build the APK
Export the required environment variables (note the required hyphen in the version name) and build the release APK using Gradle:

```bash
# In Bash/Zsh/WSL
export APK_VERSION_NAME="v1.22.1-1"
export APK_ABI="arm64-v8a"
export KEYSTORE_PASS="123456"
./gradlew assembleRelease
```
*(For Windows PowerShell, use `$env:VAR_NAME="value"` instead of `export`)*

The final plugin will be generated at `app/build/outputs/apk/release/naiveproxy-plugin-v1.22.1-1.apk`. Install this APK, configure a Naive node in NekoBox, and `yass_cli` will run successfully under the hood.
