# syntax=docker/dockerfile:1
FROM golang:1.26-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TAILWIND_VERSION=v4.3.3
RUN curl -sSL -o /usr/local/bin/tailwindcss \
      https://github.com/tailwindlabs/tailwindcss/releases/download/${TAILWIND_VERSION}/tailwindcss-linux-x64 \
 && chmod +x /usr/local/bin/tailwindcss \
 && tailwindcss -i static/css/input.css -o static/css/app.css --minify
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/migrate ./cmd/migrate \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/seed ./cmd/seed

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/ /app/
COPY --from=build /src/static /app/static
EXPOSE 8080 9091
USER nonroot
ENTRYPOINT ["/app/server"]
