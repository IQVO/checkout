Feature: Policies of the saga
  ExpireOnQuoteExpiry covers OPEN and DECLINED sessions. ResolveStuckAuthorizations
  re-calls Authorize with the same key. ResolveInDoubtPlacements alerts at 20 h
  and gives up and voids at 22 h, before the 24 h backstop of customer-payments.

  Background:
    Given a checkout-ready cart with 2 lines in 2 groups
    And I open a checkout session

  Scenario: An open session expires with its price quote
    When 15 minutes pass
    And ExpireOnQuoteExpiry runs
    And I read the session
    Then the response field "state" is EXPIRED
    And the events published are "CheckoutOpened, CheckoutSessionExpired"

  Scenario: A declined session expires with its price quote too
    Given customer-payments declines with "do-not-honor"
    And I confirm the session with token "tok_1"
    When 15 minutes pass
    And ExpireOnQuoteExpiry runs
    And I read the session
    Then the response field "state" is EXPIRED

  Scenario: An expired session cannot be confirmed
    When 15 minutes pass
    And ExpireOnQuoteExpiry runs
    And I confirm the session with token "tok_1"
    Then the API answers 409
    And the problem type is "session-closed"

  Scenario: A stuck authorization is re-called with the same key and carries on
    Given customer-payments is unreachable for authorizations
    When I confirm the session with token "tok_1"
    Then the API answers 202
    And the response field "state" is AUTHORIZING
    When customer-payments recovers
    And 3 minutes pass
    And ResolveStuckAuthorizations runs
    And I read the session
    Then the response field "state" is COMPLETED
    And the authorization keys were "sess-1:auth:0, sess-1:auth:0"

  Scenario: An in-doubt placement alerts at 20 hours
    Given customer-ordering times out
    And I confirm the session with token "tok_1"
    When 20 hours pass
    And ResolveInDoubtPlacements runs
    Then alert "placement-in-doubt-20h" was raised
    And no void was sent

  Scenario: An in-doubt placement gives up and voids at 22 hours
    Given customer-ordering times out
    And I confirm the session with token "tok_1"
    When 22 hours pass
    And ResolveInDoubtPlacements runs
    And I read the session
    Then the response field "state" is FAILED
    And the response field "failureCode" is placement-unresolved
    And the authorization "auth-sess-1:auth:0" was voided with key "sess-1:void"
    And alert "placement-given-up-22h" was raised

  Scenario: The stored placement request is purged 24 hours after the end
    When I confirm the session with token "tok_1"
    And 25 hours pass
    And PurgeStoredPlacements runs
    Then the stored placement of the session is gone
