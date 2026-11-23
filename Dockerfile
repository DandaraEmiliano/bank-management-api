FROM golang:1.26.11-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /bank-management .

FROM alpine:3.23
RUN apk add --no-cache ca-certificates
COPY --from=build /bank-management /usr/local/bin/bank-management
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["bank-management"]
