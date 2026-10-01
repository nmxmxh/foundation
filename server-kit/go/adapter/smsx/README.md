# SMS Gateway Adapter

`smsx` provides outbound SMS text messaging capabilities via the Twilio Messages REST API.

## Public contract

- `Sender`: universal interface for sending SMS messages.
- `TwilioSender`: concrete sender delivering SMS through Twilio with Basic Auth and circuit breaking.
- `NormalizeE164`: validates and standardizes phone numbers into E.164 international format.
- `NewAdapter`: wraps an SMS sender into an `adapter.Provider`.

## Features

1. **E.164 Phone Validation**: Normalizes phone strings into standard `+[country][number]` digits.
2. **Circuit Breaking**: Wrapped in `circuitbreaker.CircuitBreaker` to prevent connection exhaustion.
3. **Correlation Tracking**: Injects `X-Correlation-ID` on all outbound requests.
