FROM golang:latest as builder

WORKDIR /app

COPY ./update_service/go.mod ./update_service/go.sum ./
RUN go mod download

COPY ./update_service/ .

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main ./cmd               

FROM alpine:latest

WORKDIR /root/
COPY --from=builder /app/main .
CMD ["./main"]