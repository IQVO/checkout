Feature: Every POST takes an Idempotency-Key
  The first answer below 500 is stored for 24 hours: the same key and request
  (method, path, body) replays it with the Idempotency-Replayed header, the
  same key with another request is refused with 409 idempotency-key-reused.

  Background:
    Given a checkout-ready cart with 2 lines in 2 groups

  Scenario: A write without a key is refused
    When I POST "/checkout-sessions" without a key and body {}
    Then the API answers 400
    And the problem type is "idempotency-key-required"

  Scenario: A key that is not 1 to 128 printable ASCII characters is refused
    When I POST "/checkout-sessions" with key "café" and body {}
    Then the API answers 400
    And the problem type is "idempotency-key-invalid"

  Scenario: A replayed open returns the stored answer and opens nothing twice
    When I POST "/checkout-sessions" with key "k1" and body {"cartId":"cart-1","shopperId":"shopper-1","shipToAddressId":"addr-1"}
    And I POST "/checkout-sessions" with key "k1" and body {"cartId":"cart-1","shopperId":"shopper-1","shipToAddressId":"addr-1"}
    Then the API answers 201
    And the answer is a replay
    And 1 sessions are stored

  Scenario: A replayed confirm returns the first answer and places nothing twice
    Given I open a checkout session
    When I POST "/checkout-sessions/sess-1/confirmations" with key "c1" and body {"paymentMethodToken":"tok_1"}
    And I POST "/checkout-sessions/sess-1/confirmations" with key "c1" and body {"paymentMethodToken":"tok_1"}
    Then the API answers 200
    And the answer is a replay
    And customer-ordering received 1 call with key "sess-1"

  Scenario: The same key with another request is refused
    When I POST "/checkout-sessions" with key "k1" and body {"cartId":"cart-1","shopperId":"shopper-1","shipToAddressId":"addr-1"}
    And I POST "/checkout-sessions" with key "k1" and body {"cartId":"cart-1","shopperId":"shopper-2","shipToAddressId":"addr-1"}
    Then the API answers 409
    And the problem type is "idempotency-key-reused"

  Scenario: The same key on another route is refused and runs nothing
    When I POST "/checkout-sessions" with key "k1" and body {"cartId":"cart-1","shopperId":"shopper-1","shipToAddressId":"addr-1"}
    And I POST "/checkout-sessions/sess-1/confirmations" with key "k1" and body {"paymentMethodToken":"tok_1"}
    Then the API answers 409
    And the problem type is "idempotency-key-reused"
    And customer-ordering received 0 calls with key "sess-1"

  Scenario: A malformed body is a problem document
    When I POST "/checkout-sessions" with key "k2" and body {"unknown":1}
    Then the API answers 400
    And the problem type is "malformed-request"

  Scenario: An unknown session is not found
    When I read the session "ghost"
    Then the API answers 404
    And the problem type is "not-found"
