Feature: A checkout session accepts delivery-promise's CONSOLIDATED tier
  CONSOLIDATED is the opt-in tier of a customer-chosen delivery day (delivery-promise
  ADR 0024, customer-ordering ADR 0013). The cart relays the offer's optional
  consolidationEligible and capacityBand; the session carries them as information.
  customer-ordering takes the tier from the offerId, so the place-order request is the
  same for every tier. Unknown tiers, bands and misplaced eligibility stay contract bugs.

  Scenario: A checkout session can open with a CONSOLIDATED offer
    Given a checkout-ready cart whose second group selected the CONSOLIDATED offer "offer-2" eligible true in band TIGHT
    When I open a checkout session
    Then the API answers 201
    And the response field "state" is OPEN
    And the response field "groups.1.tier" is CONSOLIDATED
    And the response field "groups.1.consolidationEligible" is true
    And the response field "groups.1.capacityBand" is TIGHT
    And the response field "groups.0.tier" is NEXT_DAY
    And the response field "groups.0.capacityBand" is AMPLE
    And the response field "groups.0.consolidationEligible" is not present
    And the response field "totalMinor" is 4400
    And the group amounts add up to the total

  Scenario: A session without the optional fields is unchanged
    Given a checkout-ready cart with 2 lines in 2 groups
    When I open a checkout session
    Then the response field "groups.1.tier" is STANDARD
    And the response field "groups.1.consolidationEligible" is not present
    And the response field "groups.1.capacityBand" is not present

  Scenario: The fields are read back and the order is placed
    Given a checkout-ready cart whose second group selected the CONSOLIDATED offer "offer-2" eligible false in band SCARCE
    And I open a checkout session
    When I confirm the session with token "tok_1"
    Then the API answers 200
    And the response field "state" is COMPLETED
    And the response field "groups.1.consolidationEligible" is false
    And customer-ordering received 1 call with key "sess-1"
    When I read the session
    Then the response field "groups.1.tier" is CONSOLIDATED
    And the response field "groups.1.capacityBand" is SCARCE

  Scenario Outline: A selected offer that breaks the optional-field rules is a contract bug
    Given a checkout-ready cart whose second group selected the <tier> offer "offer-2" <fields>
    When I open a checkout session
    Then the API answers 502
    And the problem type is "upstream-contract-violation"
    And no session was stored

    Examples:
      | tier         | fields                          |
      | STANDARD     | eligible true in band AMPLE     |
      | CONSOLIDATED | in band FULL                    |
      | FORTNIGHTLY  | in band AMPLE                   |
