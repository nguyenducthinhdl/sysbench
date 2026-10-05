# Goal: Human
Create log generate by envoy with debug mode contain stack trace, info. One or more clients send data to envoy for /payments and /booking services
- Tech Stack: Golang Client (Logs) --> Envoy (Logs) --> Backend Services (Logs)
- All logs of client and backend services should be stored by format: Time Client-Name/IP LogMode Component MessageLog (Log body)
- All logs should be store to the hard disk in envoy-log-generator folder
- Client send 100 RPS to envoy and increase gradually to 120 RPS as peak time for each 10 min interval
- For each 10 or 15 min randomly, Client/Backend has a panic and restart
- The backend side has 0% of error rate to 10% as peack for each 15 mins interval
- Envoy ratelimiter is 109 RPS for each routing point

## Run

Docker is required. `./run.sh` builds the image and leaves the stack up. Logs are written under `logs/` in this folder. Stop with `docker compose down` or `docker-compose down`.

`SMOKE=1 ./run.sh` uses a 60 second ramp and panics after a little more than a minute, checks the logs, then stops. The real intervals are 10 and 15 minutes. The Envoy bucket holds 109 tokens and refills 109 per second, so a short burst can pass before 429s start.

Two clients together offer 100 RPS rising to 120 RPS on each routing point. Envoy allows 109 RPS on each one. The points are `POST /payments`, `POST /booking`, `GET /bookings/{id}`, `GET /payments/{id}`, `GET /users/{id}`, `PUT /order/{id}`, `OPTIONS /bookings`, and `POST /foods/data`. Backends fail 0% of requests at the start of each 15 minute window and 10% at the end. Each client and each backend panics on a random 10 or 15 minute timer and is restarted.

Client and backend lines look like:

```
2026-10-05T06:20:01.123456Z client-1/10.0.0.8 INFO http POST /payments -> 200 ({"order_id":"ord-10001","amount":12.50,"currency":"USD"})
```

A stack trace keeps that prefix and puts the Go stack inside the parentheses, across lines. When a request is not processed, the message carries `reason=`:

```
2026-10-05T06:20:01.123456Z payments/10.0.0.9 ERROR payments request failed /payments reason=charge rejected by gateway (goroutine 1 [running]:
...
)
2026-10-05T06:20:01.123456Z client-1/10.0.0.8 ERROR http POST /payments -> 429 reason=rate limit exceeded (local_rate_limited)
```

| File | Contents |
| --- | --- |
| `logs/client-1.log`, `logs/client-2.log` | DEBUG once a second with the target RPS, INFO for each 2xx, ERROR with a reason for 429, 500, 503, and connection failures |
| `logs/payments.log`, `logs/booking.log` | INFO on success, ERROR with a reason plus a stack on HTTP 500 and on panic. Booking also serves `GET /bookings/{id}` and `OPTIONS /bookings`; payments also serves `GET /payments/{id}` |
| `logs/users.log`, `logs/orders.log`, `logs/foods.log` | same pattern for `GET /users/{id}`, `PUT /order/{id}`, and `POST /foods/data` |
| `logs/envoy.log` | Envoy debug log, including `[info]` and the upstream stack on HTTP 500 |
| `logs/envoy-access.log` | one JSON access record per request |

Send one extra request while the stack is up:

```bash
curl -sS -X POST localhost:10000/payments \
  -H 'content-type: application/json' \
  -d '{"order_id":"ord-1","amount":12.5,"currency":"USD"}'

curl -sS -X POST localhost:10000/booking \
  -H 'content-type: application/json' \
  -d '{"booking_id":"bk-1","route":"SGN-HAN","passengers":1}'
```

A third client is another service like `client-1`, with its own `CLIENT_NAME` and `LOG_PATH`. Set `CLIENT_COUNT` on every client to the new total so the offered rate stays 100 to 120 per route.
