Feature: Open a checkout session
  OpenSession reads the cart, checks every selected offer is still QUOTED with
  a 2 minute margin, issues the binding price quote and opens the session.

  Scenario: A ready cart opens a session with the binding total
    Given a checkout-ready cart with 2 lines in 2 groups
    When I open a checkout session
    Then the API answers 201
    And the response field "state" is OPEN
    And the response field "totalMinor" is 4400
    And the response field "groups.0.amountMinor" is 2750
    And the response field "groups.1.amountMinor" is 1650
    And the response field "authorizationId" holds null
    And the events published are "CheckoutOpened"

  Scenario: A cart that is not ready is refused before anyone else is called
    Given a cart that is not checkout ready
    When I open a checkout session
    Then the API answers 409
    And the problem type is "cart-not-checkout-ready"
    And pricing was not called

  Scenario: An offer that expires within the margin changes nothing
    Given a checkout-ready cart with 2 lines in 2 groups
    And offer "offer-2" expires in 90 seconds
    When I open a checkout session
    Then the API answers 422
    And the problem type is "offer-not-quotable"
    And no session was stored

  Scenario: An offer that is no longer quoted changes nothing
    Given a checkout-ready cart with 2 lines in 2 groups
    And offer "offer-1" is no longer quoted
    When I open a checkout session
    Then the API answers 422
    And the problem type is "offer-not-quotable"
    And no session was stored

  Scenario: An unknown cart is a validation error
    When I open a checkout session for cart "ghost"
    Then the API answers 422
    And the problem type is "validation-failed"

  Scenario: A missing reference is a validation error
    Given a checkout-ready cart with 2 lines in 2 groups
    When I POST "/checkout-sessions" with key "k9" and body {"cartId":"cart-1"}
    Then the API answers 422
    And the problem type is "validation-failed"

  Scenario: A sibling that is down is a 503 and nothing is stored
    Given a checkout-ready cart with 2 lines in 2 groups
    And pricing is unreachable
    When I open a checkout session
    Then the API answers 503
    And the problem type is "pricing-unavailable"
    And no session was stored

  Scenario: A cart the groups of which do not cover its lines is a contract bug
    Given a cart whose groups leave a line unshipped
    When I open a checkout session
    Then the API answers 502
    And the problem type is "upstream-contract-violation"
    And alert "sibling-contract-violation" was raised
