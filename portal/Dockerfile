FROM golang:1.26-alpine AS builder

# Build arguments for network environment customization.
# Defaults use official mirrors; override via --build-arg for local/CI mirrors.
ARG APK_MIRROR=dl-cdn.alpinelinux.org
ARG GOPROXY=https://proxy.golang.org,direct

RUN sed -i "s|dl-cdn.alpinelinux.org|${APK_MIRROR}|g" /etc/apk/repositories

WORKDIR /app

ENV GOPROXY=${GOPROXY}

# Copy go mod files first
COPY go.mod go.sum ./
RUN go mod download

# Copy ALL source files including web directory (for embed)
COPY . .

# Generate tailwind.min.css if missing (it's gitignored, so won't exist in fresh clone)
RUN apk add --no-cache nodejs npm && \
    if [ ! -f web/static/css/tailwind.min.css ]; then \
      echo "[Tailwind] Generating tailwind.min.css..." && \
      cd /tmp && npm init -y >/dev/null 2>&1 && \
      npm install tailwindcss@3 daisyui@4 >/dev/null 2>&1 && \
      echo 'module.exports={content:["/app/web/templates/**/*.html"],darkMode:["class","[data-theme=dark]"],theme:{extend:{colors:{primary:"var(--primary)",secondary:"var(--text-muted)",success:"var(--success)",warning:"var(--warning)",danger:"var(--error)",info:"var(--color-info)",violet:"var(--color-violet)",cyan:"var(--color-cyan)",orange:"var(--color-orange)",indigo:"var(--color-indigo)"},backgroundImage:{"brand-gradient":"var(--brand-gradient)","banner-brand":"var(--banner-gradient-brand)","banner-blue":"var(--banner-gradient-blue)","banner-purple":"var(--banner-gradient-purple)","banner-amber":"var(--banner-gradient-amber)","banner-rose":"var(--banner-gradient-rose)","banner-teal":"var(--banner-gradient-teal)"}}},plugins:[require("daisyui")],daisyui:{prefix:"du-",themes:[{light:{"primary":"#2563eb","secondary":"#64748b","accent":"#0891b2","neutral":"#1f2937","base-100":"#ffffff","info":"#0ea5e9","success":"#16a34a","warning":"#b45309","error":"#dc2626"}},"dark"],base:false,styled:true,utils:true,logs:false}}' > tailwind.config.js && \
      echo '@tailwind base;@tailwind components;@tailwind utilities;' > input.css && \
      npx tailwindcss -i input.css -o /app/web/static/css/tailwind.min.css --minify 2>&1 | tail -3 && \
      cd /app; \
    else \
      echo "[Tailwind] tailwind.min.css already exists, skipping generation"; \
    fi

# Sanity check: tailwind.min.css should exist and be reasonable size (~50-200KB)
RUN if [ ! -f web/static/css/tailwind.min.css ]; then \
      echo "ERROR: tailwind.min.css missing after build step"; exit 1; \
    elif [ $(stat -c%s web/static/css/tailwind.min.css) -gt 500000 ]; then \
      echo "ERROR: tailwind.min.css is too large ($(stat -c%s web/static/css/tailwind.min.css) bytes). Run rebuild-tailwind.sh to regenerate."; exit 1; \
    else \
      echo "[Tailwind] OK: $(stat -c%s web/static/css/tailwind.min.css) bytes"; \
    fi

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o portal ./cmd/server

FROM alpine:3.19

# Build argument must be redeclared in this stage.
ARG APK_MIRROR=dl-cdn.alpinelinux.org

RUN sed -i "s|dl-cdn.alpinelinux.org|${APK_MIRROR}|g" /etc/apk/repositories

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/portal .

# Copy static files
COPY --from=builder /app/web ./web/

# Copy versioned database migrations (applied at startup via db.Migrate)
COPY --from=builder /app/migrations ./migrations/

EXPOSE 3001

ENTRYPOINT ["./portal"]
