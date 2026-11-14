# session_confirm_rules.feature: scenarios derived from the Checkout documentation
# for ConfirmSession, the semantic lock, the decline loop, the authorization
# mismatch and the void owed. Each scenario names the rule it pins.
Feature: The ConfirmSession rules of the Checkout documentation

  Background:
    Given a checkout-ready cart with 2 lines in 2 groups
    And I open a checkout session

  # --- the authorize step ---------------------------------------------------

  Scenario: The authorization is requested for the binding total with one allocation per group
    When I confirm the session with token "tok_1"
    Then the API answers 200
    And customer-payments was asked to authorize 4400 USD allocated "1:2750, 2:1650"
    And customer-payments received the tokens "tok_1"

  Scenario Outline: The quote must still be valid when the shopper confirms, and a refusal leaves the session OPEN
    When <seconds> seconds pass
    And I confirm the session with token "tok_1"
    Then the API answers <status>
    And customer-payments received <calls> authorization calls
    When I read the session
    Then the response field "state" is <state>

    Examples:
      | seconds | status | calls | state     |
      | 899     | 200    | 1     | COMPLETED |
      | 901     | 422    | 0     | OPEN      |

  Scenario: A declined session cannot be confirmed once its quote expired
    Given customer-payments declines with "do-not-honor"
    And I confirm the session with token "tok_1"
    When 16 minutes pass
    And I confirm the session with token "tok_2"
    Then the API answers 422
    And the problem type is "price-quote-expired"
    And customer-payments received 1 authorization call
    When I read the session
    Then the response field "state" is DECLINED

  Scenario Outline: A session that ended in a requote or a failure is closed to another confirm
    Given <outcome>
    And I confirm the session with token "tok_1"
    When I confirm the session with token "tok_2"
    Then the API answers 409
    And the problem type is "session-closed"
    And customer-payments received 1 authorization call
    And customer-ordering received 1 call with key "sess-1"

    Examples:
      | outcome                                                       |
      | customer-ordering answers a definitive 409 requoting offer "offer-9" |
      | customer-ordering rejects the request with 422                |

  Scenario: A confirm while the authorization is unknown is an illegal transition and asks nobody again
    Given customer-payments is unreachable for authorizations
    And I confirm the session with token "tok_1"
    When I confirm the session with token "tok_2"
    Then the API answers 409
    And the problem type is "illegal-transition"
    And customer-payments received 1 authorization call
    And customer-payments received the tokens "tok_1"

  Scenario: A refused confirm while PLACING asks nobody and raises nothing
    Given customer-ordering times out
    And I confirm the session with token "tok_1"
    When I confirm the session with token "tok_2"
    Then the API answers 409
    And the problem type is "placement-in-doubt"
    And customer-payments received 1 authorization call
    And customer-ordering received 1 call with key "sess-1"
    And the events published are "CheckoutOpened, CheckoutPlacementInDoubt"

  # --- a decline keeps the session open ---------------------------------------

  Scenario: A decline voids nothing, holds no authorization and raises CheckoutDeclined with the reason
    Given customer-payments declines with "do-not-honor"
    When I confirm the session with token "tok_1"
    Then the API answers 200
    And the response field "state" is DECLINED
    And the response field "authorizationId" holds null
    And no void was sent
    And customer-ordering received 0 calls
    And the events published are "CheckoutOpened, CheckoutDeclined"
    And the CheckoutDeclined event carries the reason "do-not-honor"

  Scenario: A confirm repeated with the same key is the first answer, a new key is a new attempt
    Given customer-payments declines with "insufficient-funds"
    When I POST "/checkout-sessions/sess-1/confirmations" with key "c1" and body {"paymentMethodToken":"tok_1"}
    And I POST "/checkout-sessions/sess-1/confirmations" with key "c1" and body {"paymentMethodToken":"tok_1"}
    Then the API answers 200
    And the answer is a replay
    And the response field "state" is DECLINED
    And customer-payments received 1 authorization call
    When I POST "/checkout-sessions/sess-1/confirmations" with key "c2" and body {"paymentMethodToken":"tok_1"}
    Then the API answers 200
    And the answer is not a replay
    And the response field "state" is COMPLETED
    And customer-payments received 2 authorization calls
    And the authorization keys were all different

  Scenario: A replayed confirm of an in-doubt placement returns the stored 202 and places nothing again
    Given customer-ordering times out
    When I POST "/checkout-sessions/sess-1/confirmations" with key "c1" and body {"paymentMethodToken":"tok_1"}
    Then the API answers 202
    Given customer-ordering recovers
    When I POST "/checkout-sessions/sess-1/confirmations" with key "c1" and body {"paymentMethodToken":"tok_1"}
    Then the API answers 202
    And the answer is a replay
    And the response field "state" is PLACING
    And customer-ordering received 1 call with key "sess-1"

  # --- the authorization mismatch and the void owed --------------------------------

  Scenario Outline: An authorization that is not exactly the quote total, in the quote currency, is voided
    Given <fault>
    When I confirm the session with token "tok_1"
    Then the API answers 409
    And the problem type is "authorization-mismatch"
    And the authorization "auth-sess-1:auth:0" was voided with key "sess-1:void"
    And alert "authorization-mismatch" was raised
    And customer-ordering received 0 calls
    When I read the session
    Then the response field "state" is DECLINED
    And the response field "authorizationId" holds null
    And the "CheckoutCompleted" event was published 0 times

    Examples:
      | fault                                               |
      | customer-payments authorizes one minor unit too little |
      | customer-payments authorizes in "EUR"               |

  Scenario: After a mismatch the shopper confirms again as a new attempt and the first authorization stays voided
    Given customer-payments authorizes one minor unit too much
    And I confirm the session with token "tok_1"
    When I confirm the session with token "tok_2"
    Then the API answers 200
    And the response field "state" is COMPLETED
    And the response field "authorizationId" is auth-sess-1:auth:1
    And the authorization keys were all different
    And 1 void was attempted
    And the events published are "CheckoutOpened, CheckoutDeclined, CheckoutCompleted"
    And the CheckoutDeclined event carries the reason "authorization-mismatch"

  Scenario: While the void of a mismatch is still owed no new attempt is allowed
    Given customer-payments cannot void
    And customer-payments authorizes one minor unit too much
    And I confirm the session with token "tok_1"
    Then the problem type is "authorization-mismatch"
    And no authorization was voided
    When I confirm the session with token "tok_2"
    Then the API answers 409
    And the problem type is "void-pending"
    And customer-payments received 1 authorization call
    Given customer-payments can void again
    When RetryOwedVoids handles 1 session
    And I confirm the session with token "tok_3"
    Then the API answers 200
    And the response field "state" is COMPLETED
    And the authorization keys were all different

  # --- the request itself -------------------------------------------------------

  Scenario Outline: A confirm needs a PSP token of 1 to 512 characters and a refusal calls nobody
    When <attempt>
    Then the API answers <status>
    And customer-payments received <calls> authorization calls
    When I read the session
    Then the response field "state" is <state>

    Examples:
      | attempt                                                                    | status | calls | state     |
      | I POST "/checkout-sessions/sess-1/confirmations" with key "k1" and body {} | 422    | 0     | OPEN      |
      | I confirm the session with a token of 512 characters                       | 200    | 1     | COMPLETED |
      | I confirm the session with a token of 513 characters                       | 422    | 0     | OPEN      |

  # --- the settlement of the place-order answer -------------------------------------

  Scenario: A 422 from the network with a void that keeps failing stays owed and is retried at every pass
    Given customer-payments cannot void
    And customer-ordering rejects the request with 422
    When I confirm the session with token "tok_1"
    Then the API answers 200
    And the response field "state" is FAILED
    And the response field "failureCode" is place-order-rejected
    And the response field "orderId" holds null
    And alert "place-order-rejected" was raised
    And no authorization was voided
    And 1 void was attempted
    When 1 minutes pass
    And RetryOwedVoids handles 0 sessions
    Then 2 voids were attempted
    Given customer-payments can void again
    When RetryOwedVoids handles 1 session
    Then the authorization "auth-sess-1:auth:0" was voided with key "sess-1:void"
    And 3 voids were attempted
    When RetryOwedVoids handles 0 sessions
    Then 3 voids were attempted

  Scenario: A completed purchase tells the cart and payments the order, the authorization and the allocation per group
    When I confirm the session with token "tok_1"
    Then the CheckoutCompleted event names order "order-sess-1", shopper "shopper-1", cart "cart-1" and authorization "auth-sess-1:auth:0" for 4400 USD allocated "1:2750, 2:1650"
    When I read the session
    Then the response field "authorizationId" is auth-sess-1:auth:0
    And the response field "orderId" is order-sess-1
    And the response field "failureCode" holds null
    And the response field "version" is 4

  Scenario: A requote carries the re-quoted offers, only in REQUOTE_REQUIRED
    Then the response has no field "requotedOffers"
    Given customer-ordering answers a definitive 409 requoting offer "offer-9"
    When I confirm the session with token "tok_1"
    Then the response field "state" is REQUOTE_REQUIRED
    And the response field "requotedOffers.0.offerId" is offer-9
    And the response field "requotedOffers.0.groupNo" is 1
    And the response field "requotedOffers.0.tier" is SAME_DAY
    And the response field "requotedOffers.0.deliverByEarliest" is 2026-10-26
    And the response field "requotedOffers.0.deliverByLatest" is 2026-10-27
    And the response field "requotedOffers.0.expiresAt" is 2026-10-25T11:00:00Z
    And the CheckoutRequoteRequired event carries offer "offer-9" for group 1
    When I open a checkout session
    Then the API answers 201
    And the response field "sessionId" is sess-2
    And the response has no field "requotedOffers"

  Scenario: The PSP token never leaves the session while the authorization is unknown
    Given customer-payments is unreachable for authorizations
    When I confirm the session with token "tok_secret_1"
    Then the API answers 202
    And the response field "state" is AUTHORIZING
    And the response body does not contain "tok_secret_1"
    When I read the session
    Then the response body does not contain "tok_secret_1"
    And the response field "authorizationId" holds null

  Scenario Outline: The PSP token is kept only while the session is AUTHORIZING
    Given <setup>
    When I confirm the session with token "tok_1"
    Then <token>

    Examples:
      | setup                                               | token                                   |
      | customer-payments is unreachable for authorizations | the session keeps the PSP token "tok_1" |
      | customer-payments declines with "do-not-honor"      | the session keeps no PSP token          |
      | customer-ordering times out                         | the session keeps no PSP token          |
      | customer-payments authorizes one minor unit too much | the session keeps no PSP token         |
      | customer-ordering recovers                          | the session keeps no PSP token          |
