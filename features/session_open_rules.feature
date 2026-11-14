# session_open_rules.feature: scenarios derived from the Checkout documentation
# (warehouse-docs storefront/docs/contexts/checkout: use-cases, aggregate design
# canvas, business context; and the in-repo docs/docs/ddd/use-cases.md and
# docs/adr/0002, 0003). Each scenario names the rule it pins.
Feature: The OpenSession rules of the Checkout documentation

  Scenario Outline: A selected offer must be quoted for MORE than the 2 minute margin
    Given a checkout-ready cart with 2 lines in 2 groups
    And offer "offer-2" expires in <seconds> seconds
    When I open a checkout session
    Then the API answers <status>
    And <sessions> sessions are stored

    Examples:
      | seconds | status | sessions |
      | 121     | 201    | 1        |
      | 120     | 422    | 0        |
      | 119     | 422    | 0        |

  Scenario Outline: A sibling that is unreachable is a 503 and never an unquotable offer or a ready cart
    Given a checkout-ready cart with 2 lines in 2 groups
    And <outage> is unreachable
    When I open a checkout session
    Then the API answers 503
    And the problem type is "<slug>"
    And no session was stored
    And pricing was not called

    Examples:
      | outage           | slug                         |
      | shopping-cart    | shopping-cart-unavailable    |
      | delivery-promise | delivery-promise-unavailable |

  Scenario: Pricing refusing the quote request is a 422 and opens nothing
    Given a checkout-ready cart with 2 lines in 2 groups
    And pricing refuses the quote request
    When I open a checkout session
    Then the API answers 422
    And the problem type is "price-quote-rejected"
    And no session was stored

  Scenario: A retry with the same key after a 503 is executed, not replayed, and gets the same quote key
    Given a checkout-ready cart with 2 lines in 2 groups
    And pricing is unreachable
    When I POST "/checkout-sessions" with key "open-77" and body {"cartId":"cart-1","shopperId":"shopper-1","shipToAddressId":"addr-1"}
    Then the API answers 503
    And the problem type is "pricing-unavailable"
    Given pricing recovers
    When I POST "/checkout-sessions" with key "open-77" and body {"cartId":"cart-1","shopperId":"shopper-1","shipToAddressId":"addr-1"}
    Then the API answers 201
    And the answer is not a replay
    And 1 sessions are stored
    And pricing received the keys "open-77:price-quote, open-77:price-quote"

  Scenario Outline: The pricing key is the request key plus a suffix, hashed when it would pass 128 characters
    Given a checkout-ready cart with 2 lines in 2 groups
    When I POST "/checkout-sessions" with a key of <length> characters and body {"cartId":"cart-1","shopperId":"shopper-1","shipToAddressId":"addr-1"}
    Then the API answers 201
    And <rule>

    Examples:
      | length | rule                                                                      |
      | 116    | the pricing key is the request key followed by ":price-quote"             |
      | 117    | the pricing key is the SHA-256 of the request key followed by ":price-quote" |

  Scenario: Lines are numbered 1..n in cart order and every group keeps the tier and lines it ships
    Given a checkout-ready cart with one group per line for "RJ-TSHIRT-M x1, RJ-SOCKS-L x2, RJ-CAP-OS x1"
    When I open a checkout session
    Then the API answers 201
    And the response field "lines.0.lineNo" is 1
    And the response field "lines.1.lineNo" is 2
    And the response field "lines.1.catalogItemId" is RJ-SOCKS-L
    And the response field "lines.1.quantity" is 2
    And the response field "lines.2.lineNo" is 3
    And the response field "groups.0.tier" is NEXT_DAY
    And the response field "groups.1.tier" is STANDARD
    And the response field "groups.2.tier" is SAME_DAY
    And the response field "groups.2.groupNo" is 3
    And the response field "groups.2.lineNos.0" is 3

  Scenario: Each group is allocated its items, its delivery charge and its pro-rata share of the tax
    Given a checkout-ready cart with one group per line for "RJ-TSHIRT-M x1, RJ-SOCKS-L x2, RJ-CAP-OS x1"
    When I open a checkout session
    Then the response field "totalMinor" is 6050
    And the response field "currency" is USD
    And the response field "groups.0.amountMinor" is 1650
    And the response field "groups.1.amountMinor" is 2750
    And the response field "groups.2.amountMinor" is 1650
    And the group amounts add up to the total
    And the response field "quoteExpiresAt" is 2026-10-25T10:15:00Z

  Scenario: The tax that does not divide goes to the first group so the allocations still add up to the total
    Given a checkout-ready cart with 2 lines in 2 groups
    And pricing adds one minor unit of tax
    When I open a checkout session
    Then the API answers 201
    And the response field "totalMinor" is 4401
    And the response field "groups.0.amountMinor" is 2751
    And the response field "groups.1.amountMinor" is 1650
    And the group amounts add up to the total

  Scenario Outline: A sibling answer that cannot form a consistent session is a contract bug, alerted, and opens nothing
    Given a checkout-ready cart with 2 lines in 2 groups
    And <fault>
    When I open a checkout session
    Then the API answers 502
    And the problem type is "upstream-contract-violation"
    And alert "sibling-contract-violation" was raised
    And no session was stored

    Examples:
      | fault                                         |
      | pricing answers a total of 4399 minor units   |
      | pricing leaves out the price of the last line |
      | a cart whose second group claims line 1 again |
      | a cart whose second group names line 3        |

  Scenario: Every purchase attempt is its own session and each one raises CheckoutOpened
    Given a checkout-ready cart with 2 lines in 2 groups
    When I open a checkout session
    Then the response field "sessionId" is sess-1
    When I open a checkout session
    Then the API answers 201
    And the response field "sessionId" is sess-2
    And the response field "state" is OPEN
    And 2 sessions are stored
    And the "CheckoutOpened" event was published 2 times
    And the CheckoutOpened event names cart "cart-1", shopper "shopper-1" and total 4400

  Scenario Outline: Every reference of the open request is required, and a request without one calls no sibling
    Given a checkout-ready cart with 2 lines in 2 groups
    When I POST "/checkout-sessions" with key "k-open" and body <body>
    Then the API answers 422
    And the problem type is "validation-failed"
    And no session was stored
    And pricing was not called

    Examples:
      | body                                                           |
      | {"shopperId":"shopper-1","shipToAddressId":"addr-1"}           |
      | {"cartId":"cart-1","shopperId":"","shipToAddressId":"addr-1"}  |
      | {"cartId":"cart-1","shopperId":"shopper-1"}                    |

  Scenario Outline: A reference that is not a valid id is a validation error and opens nothing
    Given a checkout-ready cart with 2 lines in 2 groups
    When I POST "/checkout-sessions" with key "k-open" and body <body>
    Then the API answers 422
    And the problem type is "validation-failed"
    And no session was stored

    Examples:
      | body                                                                   |
      | {"cartId":"cart-1","shopperId":"shopper 1","shipToAddressId":"addr-1"} |
      | {"cartId":"cart-1","shopperId":"shopper-1","shipToAddressId":"addr_1!"} |
