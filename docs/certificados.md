# Certificados TLS en el puesto del cliente

Si la aplicación web se sirve por HTTPS, el navegador bloquea `ws://`. Al abrir el ejecutable, sin argumentos, el gateway crea una autoridad local y un certificado para `127.0.0.1`, pide permiso una vez y escucha en `wss://127.0.0.1:8080/ws`.

## Al abrir el programa

Doble clic en `gateway` o `gateway.exe`. No hay comandos.

El sistema pide confirmación una sola vez, y solo si esa autoridad todavía no está instalada:

- Windows muestra el aviso del almacén de certificados del usuario. Chrome y Edge confían en ese almacén. Si una política lo bloquea, aparece el control de cuentas (UAC) y la autoridad se instala para la máquina.
- macOS pide la contraseña del llavero de la sesión.
- Linux muestra el diálogo de PolicyKit. En un equipo sin PolicyKit el proceso arranca igual y el log indica que el navegador aún no confía en el certificado.

Si se cancela el aviso, el gateway igual queda en HTTPS. El navegador puede mostrar su propio aviso hasta que se acepte la autoridad.

El navegador se abre en `https://127.0.0.1:8080`. La aplicación React usa:

```javascript
const socket = new WebSocket("wss://127.0.0.1:8080/ws");
```

Los archivos quedan en la carpeta de datos del usuario, con permiso de lectura solo para esa cuenta. Cada puesto tiene su propia autoridad; no se copia a otra máquina.

- Windows: `%AppData%\HE Gateway`
- macOS: `~/Library/Application Support/HE Gateway`
- Linux: `$XDG_DATA_HOME/he-gateway` o `~/.local/share/he-gateway`

Ahí están `ca.pem`, `ca-key.pem`, `cert.pem` y `key.pem`. El certificado del servidor incluye `localhost` y `127.0.0.1`. Cuando le quedan menos de 30 días, el gateway lo renueva con la misma autoridad, sin otro aviso.

Firefox no usa el almacén del sistema. En Windows el navegador del puesto es Chrome o Edge.

`-http` deja el proceso en `ws://`, sin generar certificados. `-cert` y `-key`, los dos juntos, usan unos PEM ya creados y no tocan el almacén.

## Alternativa manual

Si hace falta un certificado propio, se puede crear con mkcert u OpenSSL y pasarlo al arrancar. Los dos flags van juntos. Si falta uno, el proceso termina con `cert and key must be set together`. La clave va en PEM y sin contraseña. El nombre del certificado tiene que cubrir `127.0.0.1` y `localhost`.

### mkcert

mkcert crea una autoridad local, la instala en el almacén del sistema y firma un certificado que Chrome, Edge y Safari aceptan. Así el `wss://` abierto desde la web en la nube no muestra un aviso. En Firefox hace falta el paquete NSS (`nss` o `libnss3-tools`) antes de `mkcert -install`.

La instalación de la CA se hace una vez por máquina. El certificado del gateway se puede regenerar cuando caduque.

### macOS

```bash
brew install mkcert nss
mkcert -install
mkdir -p certs
mkcert -key-file certs/key.pem -cert-file certs/cert.pem 127.0.0.1 localhost
```

### Linux

En Debian y Ubuntu:

```bash
sudo apt install libnss3-tools
sudo apt install mkcert
mkcert -install
mkdir -p certs
mkcert -key-file certs/key.pem -cert-file certs/cert.pem 127.0.0.1 localhost
chmod 600 certs/key.pem
```

Si el paquete `mkcert` no está en la distribución, baja el binario de la [última release](https://github.com/FiloSottile/mkcert/releases) (`linux-amd64`, o `linux-arm64` en una Raspberry Pi de 64 bits), márcalo ejecutable y colócalo en `/usr/local/bin/mkcert`. Después corre los mismos `mkcert -install` y `mkcert -key-file`.

### Windows

No hay instalador MSI. La release publica un ejecutable solo, sin dependencias: [mkcert-v1.4.4-windows-amd64.exe](https://github.com/FiloSottile/mkcert/releases/download/v1.4.4/mkcert-v1.4.4-windows-amd64.exe). Se copia al puesto y se corre desde esa carpeta. En PowerShell:

```powershell
New-Item -ItemType Directory -Force certs | Out-Null
.\mkcert-v1.4.4-windows-amd64.exe -install
.\mkcert-v1.4.4-windows-amd64.exe -key-file certs\key.pem -cert-file certs\cert.pem 127.0.0.1 localhost
```

`-install` pide permisos de administrador la primera vez: mete la autoridad en el almacén de Windows, que es el que usan Chrome y Edge.

Si se prefiere tener el comando `mkcert` en el PATH, `winget install --id FiloSottile.mkcert -e` instala ese mismo ejecutable. Chocolatey (`choco install mkcert`) y Scoop hacen lo mismo.

### OpenSSL

Si no se puede instalar mkcert, OpenSSL genera un certificado autofirmado. El navegador no confía en él hasta que alguien acepte el aviso en ese puesto.

macOS y Linux suelen traer `openssl`. En Windows está en Git Bash, o se instala con `choco install openssl`.

```bash
mkdir -p certs
openssl req -x509 -newkey rsa:2048 -nodes \
  -keyout certs/key.pem -out certs/cert.pem -days 365 \
  -subj "/CN=127.0.0.1" \
  -addext "subjectAltName=DNS:localhost,IP:127.0.0.1"
```

`-nodes` deja la clave sin passphrase, que es lo que espera el gateway. En Linux:

```bash
chmod 600 certs/key.pem
```

Antes de usar la web en la nube, abre `https://127.0.0.1:8080` en el mismo navegador y acepta el aviso. Sin ese paso, el `wss://` desde la página HTTPS falla. Hay que repetirlo en cada navegador del puesto. mkcert evita ese aviso.

## Arrancar el gateway

Desde la carpeta donde están los PEM, con el binario del puesto:

```bash
./gateway -addr 127.0.0.1:8080 -cert certs/cert.pem -key certs/key.pem
```

En Windows:

```powershell
.\gateway.exe -addr 127.0.0.1:8080 -cert certs\cert.pem -key certs\key.pem
```

En desarrollo, con el código en esa máquina:

```bash
go run ./cmd/gateway -addr 127.0.0.1:8080 -cert certs/cert.pem -key certs/key.pem
```

Las rutas pueden ser absolutas. El dashboard queda en `https://127.0.0.1:8080`. El dashboard usa `wss` solo porque la página es HTTPS. La aplicación React tiene que abrir el socket así:

```javascript
const socket = new WebSocket("wss://127.0.0.1:8080/ws");
```

El resto del protocolo es el de [integracion.md](integracion.md).
