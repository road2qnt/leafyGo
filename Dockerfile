FROM golang:1.27-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /server ./cmd/server && CGO_ENABLED=0 go build -o /seed ./cmd/seed

FROM alpine:3.20
RUN adduser -D app
USER app
COPY --from=build /server /server
COPY --from=build /seed /seed
EXPOSE 8080
ENTRYPOINT ["/server"]
