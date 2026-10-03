# Compilación y ejecución

Go solo se instala en la máquina donde se compila. El resultado es un ejecutable estático: en el puesto del cliente se copia ese archivo y se arranca. No hace falta el código fuente ni el toolchain.

El módulo pide Go 1.25 o posterior. `CGO_ENABLED=0` evita dependencias de C, así que se puede cruzar de sistema sin un compilador cruzado.

Comprueba el toolchain en la máquina de build:

```bash
go version
```

Desde la raíz del repositorio, estos comandos generan un binario por destino.

## Compilar desde macOS o Linux

```bash
mkdir -p bin

# La propia máquina (macOS o Linux, la arquitectura del host)
CGO_ENABLED=0 go build -o bin/gateway ./cmd/gateway

# Windows 64 bits
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o bin/gateway.exe ./cmd/gateway

# Linux de escritorio o servidor 64 bits
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/gateway-linux-amd64 ./cmd/gateway

# Raspberry Pi 4, Pi 5 y Pi Zero 2 con sistema de 64 bits
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o bin/gateway-linux-arm64 ./cmd/gateway

# Raspberry Pi OS de 32 bits
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -o bin/gateway-linux-armv7 ./cmd/gateway

# Mac con Apple Silicon
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o bin/gateway-darwin-arm64 ./cmd/gateway

# Mac Intel
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o bin/gateway-darwin-amd64 ./cmd/gateway
```

## Compilar desde Windows

En PowerShell, desde la raíz del repositorio:

```powershell
New-Item -ItemType Directory -Force bin | Out-Null

# El propio Windows
$env:CGO_ENABLED="0"; $env:GOOS="windows"; $env:GOARCH="amd64"
go build -o bin/gateway.exe ./cmd/gateway

# Linux 64 bits
$env:CGO_ENABLED="0"; $env:GOOS="linux"; $env:GOARCH="amd64"
go build -o bin/gateway-linux-amd64 ./cmd/gateway

# Raspberry Pi 64 bits
$env:CGO_ENABLED="0"; $env:GOOS="linux"; $env:GOARCH="arm64"
go build -o bin/gateway-linux-arm64 ./cmd/gateway

# Raspberry Pi OS 32 bits
$env:CGO_ENABLED="0"; $env:GOOS="linux"; $env:GOARCH="arm"; $env:GOARM="7"
go build -o bin/gateway-linux-armv7 ./cmd/gateway
```

En `cmd.exe` el equivalente es `set CGO_ENABLED=0`, `set GOOS=linux` y `set GOARCH=amd64` antes de `go build`.

| Destino | `GOOS` | `GOARCH` | Archivo |
| --- | --- | --- | --- |
| Windows 64 bits | `windows` | `amd64` | `gateway.exe` |
| Linux 64 bits | `linux` | `amd64` | `gateway-linux-amd64` |
| Raspberry Pi 64 bits | `linux` | `arm64` | `gateway-linux-arm64` |
| Raspberry Pi 32 bits | `linux` | `arm` (`GOARM=7`) | `gateway-linux-armv7` |
| macOS Apple Silicon | `darwin` | `arm64` | `gateway-darwin-arm64` |
| macOS Intel | `darwin` | `amd64` | `gateway-darwin-amd64` |

Copia solo el binario del sistema de destino. Sin `-http` ni `-cert`, el proceso genera un certificado local y el dashboard queda en [https://127.0.0.1:8080](https://127.0.0.1:8080), con el WebSocket en `wss://127.0.0.1:8080/ws`. El permiso del sistema se pide una vez; los pasos están en [certificados.md](certificados.md). `-http` conserva `http://127.0.0.1:8080`. `Ctrl+C` cierra el HTTP, los sockets y los puertos seriales. `-h` lista los flags.

El proceso escucha en loopback. El navegador tiene que estar en la misma máquina. Los nombres de puerto cambian según el sistema: `COM3` en Windows, `/dev/ttyUSB0` o `/dev/ttyACM0` en Linux, `/dev/cu.usbserial-XXXX` en macOS.

## macOS

Compila en el propio Mac, desde la raíz del repositorio. Go elige la arquitectura del equipo (Apple Silicon o Intel):

```bash
mkdir -p bin
CGO_ENABLED=0 go build -o bin/gateway ./cmd/gateway
./bin/gateway -addr 127.0.0.1:8080 -baud 9600 -devices /dev/cu.usbserial-110
```

Para fijar la arquitectura a mano:

```bash
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o bin/gateway-darwin-arm64 ./cmd/gateway
CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build -o bin/gateway-darwin-amd64 ./cmd/gateway
```

Durante el desarrollo, `go run` compila y arranca en un solo paso:

```bash
go run ./cmd/gateway -addr 127.0.0.1:8080 -devices /dev/cu.usbserial-110
```

Si el binario viene de otra máquina, usa `gateway-darwin-arm64` o `gateway-darwin-amd64`, dale permiso de ejecución y arráncalo igual:

```bash
chmod +x gateway-darwin-arm64
./gateway-darwin-arm64 -devices /dev/cu.usbserial-110
```

Lista los puertos con el dashboard o con:

```bash
ls /dev/cu.*
```

macOS puede pedir autorización la primera vez que un binario descargado se ejecuta. Si Gatekeeper lo bloquea, ábrelo desde el Finder con clic derecho y «Abrir», o quita la cuarentena de esa copia:

```bash
xattr -d com.apple.quarantine ./gateway-darwin-arm64
```

## Linux

Compila en el propio Linux, desde la raíz del repositorio:

```bash
mkdir -p bin
CGO_ENABLED=0 go build -o bin/gateway ./cmd/gateway
./bin/gateway -addr 127.0.0.1:8080 -baud 9600 -devices /dev/ttyUSB0
```

Para un binario de 64 bits con nombre explícito:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o bin/gateway-linux-amd64 ./cmd/gateway
```

En desarrollo:

```bash
go run ./cmd/gateway -addr 127.0.0.1:8080 -devices /dev/ttyUSB0
```

Si el archivo llegó de otra máquina:

```bash
chmod +x gateway-linux-amd64
./gateway-linux-amd64 -addr 127.0.0.1:8080 -baud 9600 -devices /dev/ttyUSB0
```

Varios dispositivos van separados por coma: `-devices /dev/ttyUSB0,/dev/ttyACM0`.

El usuario que arranca el proceso tiene que poder leer y escribir el dispositivo. En la mayoría de distribuciones eso es el grupo `dialout`:

```bash
sudo usermod -aG dialout "$USER"
```

La sesión hay que iniciarla de nuevo para que el grupo aplique. Comprueba el nodo con `ls -l /dev/ttyUSB*`.

Para dejarlo en segundo plano en una terminal:

```bash
nohup ./gateway-linux-amd64 -devices /dev/ttyUSB0 > gateway.log 2>&1 &
```

## Raspberry Pi

Con Go instalado en la Pi, compila ahí mismo. `uname -m` dice qué arquitectura usar.

```bash
uname -m
cd ~/he-gateway
CGO_ENABLED=0 go build -o gateway ./cmd/gateway
./gateway -addr 127.0.0.1:8080 -baud 9600 -devices /dev/ttyUSB0
```

`aarch64` también se puede forzar así:

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o gateway ./cmd/gateway
```

En una imagen de 32 bits (`armv7l`):

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 go build -o gateway ./cmd/gateway
```

En desarrollo, sobre la Pi:

```bash
go run ./cmd/gateway -addr 127.0.0.1:8080 -devices /dev/ttyUSB0
```

Si no quieres instalar Go en la Pi, elige el binario según el sistema, no según el modelo de placa:

```bash
uname -m
```

`aarch64` usa `gateway-linux-arm64`. `armv7l` usa `gateway-linux-armv7`.

Copia el archivo desde la máquina de build:

```bash
scp bin/gateway-linux-arm64 pi@192.168.1.20:~/gateway
```

En la Pi:

```bash
chmod +x ~/gateway
~/gateway -addr 127.0.0.1:8080 -baud 9600 -devices /dev/ttyUSB0
```

El navegador y el WebSocket se usan en la propia Pi, porque la escucha es `127.0.0.1`. El permiso del puerto es el mismo que en Linux: grupo `dialout`. Los adaptadores USB CH340 suelen aparecer como `/dev/ttyUSB0`; algunos lectores aparecen como `/dev/ttyACM0`.

## Windows

Compila en el propio Windows, desde la raíz del repositorio.

PowerShell:

```powershell
New-Item -ItemType Directory -Force bin | Out-Null
$env:CGO_ENABLED="0"
$env:GOOS="windows"
$env:GOARCH="amd64"
go build -o bin/gateway.exe ./cmd/gateway
.\bin\gateway.exe -addr 127.0.0.1:8080 -baud 9600 -devices COM3
```

`cmd.exe`:

```bat
mkdir bin
set CGO_ENABLED=0
set GOOS=windows
set GOARCH=amd64
go build -o bin\gateway.exe .\cmd\gateway
bin\gateway.exe -addr 127.0.0.1:8080 -baud 9600 -devices COM3
```

En desarrollo, PowerShell:

```powershell
go run .\cmd\gateway -addr 127.0.0.1:8080 -devices COM3
```

Si `gateway.exe` ya viene compilado de otra máquina, ábrelo en PowerShell o en el símbolo del sistema:

```powershell
.\gateway.exe -addr 127.0.0.1:8080 -baud 9600 -devices COM3
```

En `cmd.exe`:

```bat
gateway.exe -addr 127.0.0.1:8080 -baud 9600 -devices COM3
```

Varios puertos: `-devices COM3,COM4`. El Administrador de dispositivos muestra el número COM del lector o del relé. La ventana tiene que seguir abierta: cerrarla detiene el gateway. `Ctrl+C` también lo apaga.

Como escucha solo en `127.0.0.1`, el navegador del mismo PC abre [http://127.0.0.1:8080](http://127.0.0.1:8080). Otro equipo de la red no llega a ese puerto.
