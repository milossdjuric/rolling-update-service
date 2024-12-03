FROM golang:latest as builder

WORKDIR /app

COPY ./rolling_update_service/go.mod ./rolling_update_service/go.sum ./

COPY ./magnetar ../magnetar

RUN go mod download

COPY ./rolling_update_service/ .

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main ./cmd               

FROM alpine:latest

WORKDIR /root/
COPY --from=builder /app/main .
CMD ["./main"]