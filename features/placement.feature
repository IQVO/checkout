Feature: The place-order step and the semantic lock
  PLACING is the lock: while the outcome is unknown there is no void and no
  new attempt. A definitive 409 voids and asks for a requote; a 422 is a
  contract bug: void, FAILED, alert.

  Background:
    Given a checkout-ready cart with 2 lines in 2 groups
    And I open a checkout session

  Scenario: A timeout leaves the session PLACING and answers 202
    Given customer-ordering times out
    When I confirm the session with token "tok_1"
    Then the API answers 202
    And the response field "state" is PLACING
    And no void was sent
    And the events published are "CheckoutOpened, CheckoutPlacementInDoubt"

  Scenario: There is no new attempt while the outcome is unknown
    Given customer-ordering times out
    And I confirm the session with token "tok_1"
    When I confirm the session with token "tok_2"
    Then the API answers 409
    And the problem type is "placement-in-doubt"
    And no void was sent

  Scenario: The BFF polls the session until the resolver settles it
    Given customer-ordering times out
    And I confirm the session with token "tok_1"
    When customer-ordering recovers
    And 1 minutes pass
    And ResolveInDoubtPlacements runs
    And I read the session
    Then the API answers 200
    And the response field "state" is COMPLETED
    And customer-ordering received 2 calls with key "sess-1"
    And both placement calls carried the same body

  Scenario: A definitive 409 voids the authorization and asks for a requote
    Given customer-ordering answers a definitive 409 requoting offer "offer-9"
    When I confirm the session with token "tok_1"
    Then the API answers 200
    And the response field "state" is REQUOTE_REQUIRED
    And the response field "requotedOffers.0.offerId" is offer-9
    And the authorization "auth-sess-1:auth:0" was voided with key "sess-1:void"
    And the events published are "CheckoutOpened, CheckoutRequoteRequired"

  Scenario: A 422 is a contract bug that voids, fails and alerts
    Given customer-ordering rejects the request with 422
    When I confirm the session with token "tok_1"
    Then the API answers 200
    And the response field "state" is FAILED
    And the response field "failureCode" is place-order-rejected
    And the authorization "auth-sess-1:auth:0" was voided with key "sess-1:void"
    And alert "place-order-rejected" was raised
    And the events published are "CheckoutOpened, CheckoutFailed"

  Scenario: A void that fails stays owed and is retried until it succeeds
    Given customer-payments cannot void
    And customer-ordering answers a definitive 409 requoting offer "offer-9"
    When I confirm the session with token "tok_1"
    Then the response field "state" is REQUOTE_REQUIRED
    And no authorization was voided
    When customer-payments can void again
    And RetryOwedVoids runs
    Then the authorization "auth-sess-1:auth:0" was voided with key "sess-1:void"
