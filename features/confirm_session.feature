Feature: Confirm a checkout session
  Authorize before placing (compensatable: void), place the order (the pivot).
  A decline keeps the session open for another token while the quote is valid.

  Background:
    Given a checkout-ready cart with 2 lines in 2 groups
    And I open a checkout session

  Scenario: Authorize then place completes the purchase
    When I confirm the session with token "tok_1"
    Then the API answers 200
    And the response field "state" is COMPLETED
    And the response field "orderId" is order-sess-1
    And the authorization keys were "sess-1:auth:0"
    And customer-ordering received 1 call with key "sess-1"
    And the events published are "CheckoutOpened, CheckoutCompleted"

  Scenario: A decline keeps the session open for another token
    Given customer-payments declines with "insufficient-funds"
    When I confirm the session with token "tok_1"
    Then the API answers 200
    And the response field "state" is DECLINED
    When I confirm the session with token "tok_2"
    Then the API answers 200
    And the response field "state" is COMPLETED
    And the authorization keys were "sess-1:auth:0, sess-1:auth:1"
    And the events published are "CheckoutOpened, CheckoutDeclined, CheckoutCompleted"

  Scenario: Confirming after the quote expired is refused
    When 16 minutes pass
    And I confirm the session with token "tok_1"
    Then the API answers 422
    And the problem type is "price-quote-expired"

  Scenario: A closed session never changes
    When I confirm the session with token "tok_1"
    And I confirm the session with token "tok_2"
    Then the API answers 409
    And the problem type is "session-closed"

  Scenario: An authorized amount that is not the quote total is voided
    Given customer-payments authorizes one minor unit too much
    When I confirm the session with token "tok_1"
    Then the API answers 409
    And the problem type is "authorization-mismatch"
    And the authorization "auth-sess-1:auth:0" was voided with key "sess-1:void"
    And alert "authorization-mismatch" was raised
    And customer-ordering received 0 calls

  Scenario: A missing token is a validation error
    When I POST "/checkout-sessions/sess-1/confirmations" with key "k5" and body {}
    Then the API answers 422
    And the problem type is "validation-failed"

  Scenario: An unknown session is not found
    When I POST "/checkout-sessions/ghost/confirmations" with key "k6" and body {"paymentMethodToken":"tok"}
    Then the API answers 404
    And the problem type is "not-found"
