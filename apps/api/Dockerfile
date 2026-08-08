# ==============================================================================
# Backend Go — imagen de PRODUCCIÓN
#
# Build multi-etapa: compilamos con el toolchain completo y publicamos sólo el
# binario. La imagen final no lleva compilador ni código fuente.
#
# OJO con CGO: el cliente de TigerBeetle enlaza una librería nativa en C, así
# que NO se puede compilar con CGO_ENABLED=0. Por eso el builder instala gcc y
# la imagen final es Alpine (con libc) en lugar de scratch.
# ==============================================================================

# ------------------------------------------------------------------------------
# Etapa 1: compilación
# ------------------------------------------------------------------------------
FROM golang:1.26-alpine AS builder

# gcc y musl-dev son necesarios para CGO (cliente de TigerBeetle).
RUN apk add --no-cache gcc musl-dev

WORKDIR /build

# Copiamos primero los manifiestos de dependencias. Docker cachea esta capa y
# sólo la invalida si cambian go.mod o go.sum, no en cada cambio de código.
COPY apps/api/go.mod apps/api/go.sum ./
RUN go mod download

COPY apps/api/ ./

# CGO_ENABLED=1 es obligatorio por TigerBeetle.
# -ldflags="-s -w" quita la tabla de símbolos y la info de debug: la imagen
# resultante pesa bastante menos.
RUN CGO_ENABLED=1 GOOS=linux go build \
        -ldflags="-s -w" \
        -o /build/bin/api \
        ./cmd/api

# ------------------------------------------------------------------------------
# Etapa 2: imagen final
# ------------------------------------------------------------------------------
FROM alpine:3.21

# ca-certificates: necesario para las llamadas HTTPS a OpenRouter.
# tzdata: para que los timestamps se interpreten en la zona horaria correcta.
RUN apk add --no-cache ca-certificates tzdata

# La aplicación corre como usuario sin privilegios. Si alguien logra ejecutar
# código dentro del contenedor, no lo hace como root.
RUN addgroup -g 1001 -S banca && \
    adduser -u 1001 -S banca -G banca

WORKDIR /app

COPY --from=builder /build/bin/api /app/api
COPY --from=builder /build/migrations /app/migrations

USER banca

EXPOSE 8080

ENTRYPOINT ["/app/api"]
