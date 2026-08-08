# ==============================================================================
# Frontend — imagen de PRODUCCIÓN
#
# Compila la aplicación y la sirve con nginx. La imagen final sólo contiene
# archivos estáticos: ni Node, ni código fuente, ni dependencias.
# ==============================================================================

# ------------------------------------------------------------------------------
# Etapa 1: compilación
# ------------------------------------------------------------------------------
FROM node:22-alpine AS builder

RUN corepack enable && corepack prepare pnpm@10.32.1 --activate

WORKDIR /app

COPY apps/web/package.json apps/web/pnpm-lock.yaml* ./
RUN pnpm install --frozen-lockfile

COPY apps/web/ ./

# Vite reemplaza las variables VITE_* literalmente en el bundle durante el
# build, no en tiempo de ejecución. Por eso llegan como build args y no como
# variables de entorno del contenedor final.
ARG VITE_API_URL
ARG VITE_WS_URL
ENV VITE_API_URL=$VITE_API_URL
ENV VITE_WS_URL=$VITE_WS_URL

RUN pnpm build

# ------------------------------------------------------------------------------
# Etapa 2: servidor estático
# ------------------------------------------------------------------------------
FROM nginx:1.27-alpine

COPY apps/web/nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=builder /app/dist /usr/share/nginx/html

EXPOSE 80

CMD ["nginx", "-g", "daemon off;"]
