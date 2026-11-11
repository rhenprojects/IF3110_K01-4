# Stage 1: Build static assets
FROM node:22-alpine AS build

WORKDIR /app

# Cache dependencies
COPY package.json package-lock.json ./
RUN npm ci

# Salin source code
COPY . .

# Target stage untuk pengetesan / typecheck
FROM build AS test
RUN npm run typecheck

# Build frontend untuk production
FROM build AS prod-build
RUN npm run build

# Stage 2: Serve dengan Nginx
FROM nginx:alpine

# Salin konfigurasi Nginx untuk SPA dan reverse proxy API
COPY nginx.conf /etc/nginx/conf.d/default.conf

# Salin static file hasil build
COPY --from=prod-build /app/dist /usr/share/nginx/html

EXPOSE 80

CMD ["nginx", "-g", "daemon off;"]

