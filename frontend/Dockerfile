# Build stage
FROM node:22-bookworm AS builder

WORKDIR /app

COPY package*.json ./
COPY scripts/patch-resumablejs.js ./scripts/patch-resumablejs.js
RUN npm ci --legacy-peer-deps

COPY . .

RUN if [ ! -f .env ]; then \
    echo "NODE_MAX_MEMORY=4096" > .env && \
    echo "GENERATE_SOURCEMAP=false" >> .env && \
    echo "WEBPACK_PARALLEL_BUILD=false" >> .env; \
    fi

RUN sed -i 's/\r$//' .env
RUN export $(cat .env | grep -v '^#' | xargs) && \
    NODE_MAX_MEMORY=${NODE_MAX_MEMORY:-4096} && \
    GENERATE_SOURCEMAP=${GENERATE_SOURCEMAP:-false} && \
    WEBPACK_PARALLEL_BUILD=${WEBPACK_PARALLEL_BUILD:-false} && \
    export GENERATE_SOURCEMAP && \
    export WEBPACK_PARALLEL_BUILD && \
    NODE_OPTIONS=--max_old_space_size=$NODE_MAX_MEMORY npm run build

# Runtime stage
FROM nginx:stable-alpine

COPY --from=builder /app/build /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf

EXPOSE 80

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -q -O /dev/null http://127.0.0.1/favicon.png || exit 1
