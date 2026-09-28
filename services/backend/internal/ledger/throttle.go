package ledger

import (
	"net/http"
	"strconv"
	"time"
)

// DoThrottled sends a request and, when it is a GET the provider refused with 429 Too Many Requests, waits as its
// Retry-After asks, in seconds or as a date (at most 30 seconds; 2, then 4, without one), and sends it again, three
// tries in all. Writes are never repeated here: they keep their single attempt and readback.
func DoThrottled(client *http.Client, req *http.Request) (*http.Response, error) {
	for try := 1; ; try++ {
		response, err := client.Do(req)
		if err != nil || response.StatusCode != http.StatusTooManyRequests || req.Method != http.MethodGet || try == 3 {
			return response, err
		}
		wait, after := time.Duration(2*try)*time.Second, response.Header.Get("Retry-After")
		if seconds, e := strconv.Atoi(after); e == nil && seconds >= 0 {
			wait = time.Duration(seconds) * time.Second
		} else if at, e := http.ParseTime(after); e == nil {
			wait = max(time.Until(at), 0)
		}
		wait = min(wait, 30*time.Second)
		response.Body.Close()
		select {
		case <-req.Context().Done():
			return nil, req.Context().Err()
		case <-time.After(wait):
		}
	}
}
