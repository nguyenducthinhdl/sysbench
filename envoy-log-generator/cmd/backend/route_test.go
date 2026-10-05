package main

import "testing"

func TestRoutes(t *testing.T) {
	ok := []struct{ service, method, path string }{
		{"payments", "POST", "/payments"},
		{"payments", "GET", "/payments/pay-100"},
		{"booking", "POST", "/booking"},
		{"booking", "GET", "/bookings/bk-100"},
		{"booking", "OPTIONS", "/bookings"},
		{"users", "GET", "/users/usr-100"},
		{"orders", "PUT", "/order/ord-100"},
		{"foods", "POST", "/foods/data"},
	}
	for _, tc := range ok {
		if !matchEndpoint(tc.service, tc.method, tc.path) {
			t.Fatalf("want match %s %s %s", tc.service, tc.method, tc.path)
		}
	}

	if matchEndpoint("booking", "POST", "/bookings/bk-100") {
		t.Fatal("POST /booking must not swallow /bookings")
	}
	if matchEndpoint("payments", "GET", "/payments") {
		t.Fatal("GET /payments requires an id")
	}
	if matchEndpoint("foods", "POST", "/foods/other") {
		t.Fatal("foods only accepts /foods/data")
	}
	if matchEndpoint("booking", "GET", "/bookings") {
		t.Fatal("GET /bookings requires an id")
	}
	if !knownPath("booking", "/bookings") {
		t.Fatal("OPTIONS path should be known")
	}
}
