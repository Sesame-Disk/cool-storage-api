# SesameFS Frontend - Standalone React build
# Matches backend's Debian Trixie base

# Build stage
FROM node:22-bookworm AS builder

WORKDIR /app

# Copy package files
COPY package*.json ./

# Install dependencies
RUN npm ci --legacy-peer-deps

# Copy source code
COPY . .

# Build argument for API URL (can be empty for relative URLs)
ARG SESAMEFS_API_URL=
ENV SESAMEFS_API_URL=${SESAMEFS_API_URL}

# Build the application
RUN npm run build

# Production stage
FROM nginx:bookworm

# Copy built assets from builder
COPY --from=builder /app/build /usr/share/nginx/html

# Copy nginx configuration
COPY nginx.conf /etc/nginx/conf.d/default.conf

# Copy runtime config script
COPY docker-entrypoint.sh /docker-entrypoint.sh
RUN chmod +x /docker-entrypoint.sh

EXPOSE 80

ENTRYPOINT ["/docker-entrypoint.sh"]
CMD ["nginx", "-g", "daemon off;"]
