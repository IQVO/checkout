# session_api_rules.feature: the REST contract rules of the Checkout
# documentation (apis/openapi.yaml, docs/docs/api-reference/overview.md and the
# troubleshooting table) that the Idempotency-Key and request-size guards add to
# every write. Each scenario names the rule it pins.
Feature: The REST write rules of the Checkout documentation

  Background:
    Given a checkout-ready cart with 2 lines in 2 groups

  Scenario Outline: An Idempotency-Key is 1 to 128 printable ASCII characters
    When I POST "/checkout-sessions" with a key of <length> characters and body {"cartId":"cart-1","shopperId":"shopper-1","shipToAddressId":"addr-1"}
    Then the API answers <status>
    And <outcome>

    Examples:
      | length | status | outcome                                      |
      | 128    | 201    | 1 sessions are stored                        |
      | 129    | 400    | the problem type is "idempotency-key-invalid" |

  Scenario: A stored answer is replayed for 24 hours and executed afresh after that
    Given every offer stays quotable for 2 days
    When I POST "/checkout-sessions" with key "open-1" and body {"cartId":"cart-1","shopperId":"shopper-1","shipToAddressId":"addr-1"}
    Then the API answers 201
    When 23 hours and 59 minutes pass
    And I POST "/checkout-sessions" with key "open-1" and body {"cartId":"cart-1","shopperId":"shopper-1","shipToAddressId":"addr-1"}
    Then the answer is a replay
    And 1 sessions are stored
    When 2 minutes pass
    And I POST "/checkout-sessions" with key "open-1" and body {"cartId":"cart-1","shopperId":"shopper-1","shipToAddressId":"addr-1"}
    Then the API answers 201
    And the answer is not a replay
    And the response field "sessionId" is sess-2
    And 2 sessions are stored

  Scenario: An error answer below 500 is stored too, so the same request replays it even after the cause is gone
    Given a cart that is not checkout ready
    When I POST "/checkout-sessions" with key "open-2" and body {"cartId":"cart-1","shopperId":"shopper-1","shipToAddressId":"addr-1"}
    Then the API answers 409
    And the problem type is "cart-not-checkout-ready"
    Given a checkout-ready cart with 2 lines in 2 groups
    When I POST "/checkout-sessions" with key "open-2" and body {"cartId":"cart-1","shopperId":"shopper-1","shipToAddressId":"addr-1"}
    Then the API answers 409
    And the answer is a replay
    And no session was stored

  Scenario: A NUL character in a body is a malformed request that does not burn its Idempotency-Key
    When I POST "/checkout-sessions" with key "open-3" and body {"cartId":"cart\u00001","shopperId":"shopper-1","shipToAddressId":"addr-1"}
    Then the API answers 400
    And the problem type is "malformed-request"
    When I POST "/checkout-sessions" with key "open-3" and body {"cartId":"cart-1","shopperId":"shopper-1","shipToAddressId":"addr-1"}
    Then the API answers 201
    And the answer is not a replay

  Scenario Outline: A request body is limited to 64 KiB
    When I open a checkout session with a body of <bytes> bytes
    Then the API answers <status>
    And the problem type is "<slug>"

    Examples:
      | bytes | status | slug              |
      | 65536 | 422    | validation-failed |
      | 65537 | 400    | malformed-request |
