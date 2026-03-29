FROM golang:1.26 AS build

WORKDIR /src

ARG TAILWIND_VERSION=4.2.2

RUN go install github.com/a-h/templ/cmd/templ@latest

RUN ARCH=$(uname -m) && \
    case "$ARCH" in \
      x86_64) ARCH="x64" ;; \
      aarch64) ARCH="arm64" ;; \
    esac && \
    curl -fSL "https://github.com/tailwindlabs/tailwindcss/releases/download/v${TAILWIND_VERSION}/tailwindcss-linux-${ARCH}" \
    -o /usr/local/bin/tailwindcss && \
    chmod +x /usr/local/bin/tailwindcss

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN templ generate

RUN mkdir -p assets/dist && \
    tailwindcss -i assets/input.css -o assets/dist/styles.min.css --minify

RUN CGO_ENABLED=0 go build -o /app .

FROM gcr.io/distroless/static-debian12

COPY --from=build /app /app

EXPOSE 8090

CMD ["/app", "serve", "--http=0.0.0.0:8090"]
