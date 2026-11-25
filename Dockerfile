FROM node:22-alpine AS web
WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.27-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /app/internal/web/dist ./internal/web/dist
RUN CGO_ENABLED=0 go build -o /server ./cmd/server && CGO_ENABLED=0 go build -o /seed ./cmd/seed

FROM alpine:3.20
RUN adduser -D app
USER app
COPY --from=build /server /server
COPY --from=build /seed /seed
EXPOSE 8080
ENTRYPOINT ["/server"]
