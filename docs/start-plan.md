# Contexto
Eres un desarrollador Senior en Go y experto en integraciones de hardware. Mi equipo tiene una aplicación en la nube (React, Django, PostgreSQL) y necesitamos construir un "Local Hardware Gateway". 
Este gateway es un microservicio local que se ejecuta en las computadoras/terminales de los clientes (Windows/Linux/Raspberry Pi) y actúa como un puente bidireccional entre nuestra web en la nube y los dispositivos de hardware locales mediante WebSockets.

# Objetivo
Generar la estructura de código y la implementación inicial en Go para este gateway, asegurando un rendimiento alto, concurrencia segura, un diseño altamente testeable y una distribución en un solo archivo binario sin dependencias externas.

# Requerimientos Técnicos
1. **Servidor WebSocket Bidireccional:**
   - Usar `github.com/gorilla/websocket`.
   - Escuchar peticiones entrantes desde el navegador (aplicación React).
   - Estructura de mensajes en JSON. Ejemplo de entrada desde la web: `{"action": "open_door", "device_id": "COM3"}`.
2. **Gestión de Hardware (Puertos Seriales):**
   - Usar `go.bug.st/serial` para leer/escribir en puertos seriales (lectores RFID, biométricos, relés USB CH340).
   - Debe poder mantener la escucha (lectura) constante de un lector. Cuando detecte un ID, emitirá automáticamente un mensaje al WebSocket: `{"event": "hardware_read", "data": "12345678"}`.
3. **Dashboard de Administración Integrado:**
   - Usar `go:embed` para incrustar una pequeña interfaz web (HTML/JS/Tailwind) servida en un puerto HTTP (ej. `http://localhost:8080`).
   - Esta interfaz debe mostrar: Estado del WebSocket (conectado/desconectado), lista de puertos COM/TTY disponibles, y un registro (log) de eventos.

# Buenas Prácticas y Estándares de Go (Idiomatic Go)
1. **Manejo de Errores y Logs:** Evita el uso de `panic`. Retorna errores de forma explícita y envuélvelos usando `fmt.Errorf("...: %w", err)` para dar contexto. Utiliza la librería estándar `log/slog` (Go 1.21+) para logging estructurado (JSON/texto).
2. **Gestión del Ciclo de Vida (Context):** Usa `context.Context` para manejar la propagación de cancelaciones y timeouts, especialmente para detener los bucles infinitos de lectura de puertos seriales y cerrar el servidor WebSocket limpiamente.
3. **Inyección de Dependencias:** Las estructuras (`structs`) deben recibir sus dependencias (ej. interfaces para lectura de hardware, logger) en sus constructores. Evita variables globales.
4. **Graceful Shutdown:** Intercepta señales del SO (SIGINT, SIGTERM) para cerrar conexiones WebSocket, liberar puertos seriales y apagar el servidor HTTP sin corromper datos.

# Estrategia de Testing
1. **Mocking de Hardware:** Define una interfaz abstracta (ej. `type SerialDevice interface`) para interactuar con los puertos. Esto es vital para inyectar un *mock* durante las pruebas unitarias y no depender de tener un hardware físico conectado.
2. **Table-Driven Tests:** Utiliza el paquete estándar `testing` aplicando el patrón de tablas (slice de structs) para probar exhaustivamente el parseo de mensajes JSON y la lógica de validación.
3. **Tests de Integración:** Incluye un test básico usando `httptest.NewServer` para levantar el WebSocket en memoria y simular un cliente enviando un mensaje válido.

# Entregables Esperados
Por favor, genera el código siguiendo el "Standard Go Project Layout":
1. `cmd/gateway/main.go`: Punto de entrada, inicialización de componentes, inyección de dependencias y graceful shutdown.
2. `internal/websocket/server.go` y `server_test.go`: Manejo del hub, clientes WS y parseo JSON, junto con pruebas de integración.
3. `internal/hardware/serial.go` y `serial_test.go`: Definición de interfaces de hardware, implementación real con `go.bug.st/serial` y ejemplos de mocks para testing.
4. `web/index.html`: Plantilla simple embebida para el dashboard.
5. `go.mod`: Archivo de definición del módulo.

Provee el código completo para estos archivos y una breve guía de cómo ejecutar las pruebas y compilar el binario para Windows y Linux (cross-compilation).