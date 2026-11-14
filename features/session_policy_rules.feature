# session_policy_rules.feature: scenarios derived from the Checkout documentation
# for the clock-driven policies: ResolveInDoubtPlacements (backoff, 20 h alert,
# 22 h give-up), ResolveStuckAuthorizations, ExpireOnQuoteExpiry and the purge
# of the stored placement request. Each scenario names the rule it pins.
Feature: The policy rules of the Checkout documentation

  Background:
    Given a checkout-ready cart with 2 lines in 2 groups
    And I open a checkout session

  # --- ResolveInDoubtPlacements ------------------------------------------------

  Scenario: The in-doubt resolver backs off 30 seconds, doubling to a 15 minute cap, with the same body and key
    Given customer-ordering times out
    When I confirm the session with token "tok_1"
    Then the API answers 202
    When ResolveInDoubtPlacements handles 0 sessions
    And 29 seconds pass
    And ResolveInDoubtPlacements handles 0 sessions
    Then customer-ordering received 1 call with key "sess-1"
    When 1 second passes
    And ResolveInDoubtPlacements handles 1 session
    And 59 seconds pass
    And ResolveInDoubtPlacements handles 0 sessions
    And 1 second passes
    And ResolveInDoubtPlacements handles 1 session
    And 120 seconds pass
    And ResolveInDoubtPlacements handles 1 session
    And 240 seconds pass
    And ResolveInDoubtPlacements handles 1 session
    And 480 seconds pass
    And ResolveInDoubtPlacements handles 1 session
    Then customer-ordering received 6 calls with key "sess-1"
    When 899 seconds pass
    And ResolveInDoubtPlacements handles 0 sessions
    And 1 second passes
    And ResolveInDoubtPlacements handles 1 session
    Then all 7 placement calls carried key "sess-1" and the same body
    When I read the session
    Then the response field "state" is PLACING

  Scenario: The in-doubt event says since when the session has been PLACING
    Given customer-ordering times out
    When 3 minutes pass
    And I confirm the session with token "tok_1"
    Then the API answers 202
    And the "CheckoutPlacementInDoubt" event was published 1 time
    And the CheckoutPlacementInDoubt event carries placingSince "2026-10-25T10:03:00Z"

  Scenario Outline: A definitive answer found by the resolver is applied as in ConfirmSession, with the void
    Given customer-ordering times out
    And <outcome>
    And I confirm the session with token "tok_1"
    When customer-ordering recovers
    And 1 minutes pass
    And ResolveInDoubtPlacements handles 1 session
    And I read the session
    Then the response field "state" is <state>
    And the authorization "auth-sess-1:auth:0" was voided with key "sess-1:void"
    And the events published are "CheckoutOpened, CheckoutPlacementInDoubt, <event>"

    Examples:
      | outcome                                                              | state            | event                   |
      | customer-ordering answers a definitive 409 requoting offer "offer-9" | REQUOTE_REQUIRED | CheckoutRequoteRequired |
      | customer-ordering rejects the request with 422                       | FAILED           | CheckoutFailed          |

  Scenario: A placement settled by the resolver is never replayed again and CheckoutCompleted is written once
    Given customer-ordering times out
    And I confirm the session with token "tok_1"
    When customer-ordering recovers
    And 1 minutes pass
    And ResolveInDoubtPlacements handles 1 session
    And 10 minutes pass
    And ResolveInDoubtPlacements handles 0 sessions
    Then customer-ordering received 2 calls with key "sess-1"
    And the events published are "CheckoutOpened, CheckoutPlacementInDoubt, CheckoutCompleted"
    And the "CheckoutCompleted" event was published 1 time

  Scenario: No alert before 20 hours
    Given customer-ordering times out
    And I confirm the session with token "tok_1"
    When 19 hours and 59 minutes pass
    And ResolveInDoubtPlacements handles 1 session
    Then alert "placement-in-doubt-20h" was not raised
    And alert "placement-given-up-22h" was not raised

  Scenario: The 20 hour alert fires once and the session stays PLACING, unvoided, until 22 hours
    Given customer-ordering times out
    And I confirm the session with token "tok_1"
    When 20 hours pass
    And ResolveInDoubtPlacements handles 1 session
    And 1 hour and 59 minutes pass
    And ResolveInDoubtPlacements handles 1 session
    And I read the session
    Then alert "placement-in-doubt-20h" was raised 1 time
    And alert "placement-given-up-22h" was not raised
    And the response field "state" is PLACING
    And no void was sent

  Scenario: At 22 hours the last replay still wins when customer-ordering has come back
    Given customer-ordering times out
    And I confirm the session with token "tok_1"
    When 22 hours pass
    And customer-ordering recovers
    And ResolveInDoubtPlacements handles 1 session
    And I read the session
    Then the response field "state" is COMPLETED
    And the response field "orderId" is order-sess-1
    And alert "placement-given-up-22h" was not raised
    And no void was sent

  Scenario: Giving up at 22 hours records FAILED first, then the void that is retried until it succeeds
    Given customer-ordering times out
    And I confirm the session with token "tok_1"
    And customer-payments cannot void
    When 22 hours pass
    And ResolveInDoubtPlacements handles 1 session
    And I read the session
    Then the response field "state" is FAILED
    And the response field "failureCode" is placement-unresolved
    And the response field "orderId" holds null
    And alert "placement-given-up-22h" was raised 1 time
    And the events published are "CheckoutOpened, CheckoutPlacementInDoubt, CheckoutFailed"
    And the CheckoutFailed event carries the code "placement-unresolved"
    And no authorization was voided
    Given customer-payments can void again
    When RetryOwedVoids handles 1 session
    Then the authorization "auth-sess-1:auth:0" was voided with key "sess-1:void"
    When I confirm the session with token "tok_2"
    Then the API answers 409
    And the problem type is "session-closed"

  # --- ResolveStuckAuthorizations -------------------------------------------------

  Scenario: An authorization is left alone for 2 minutes and then re-called with the same key and token
    Given customer-payments is unreachable for authorizations
    And I confirm the session with token "tok_1"
    When 119 seconds pass
    And ResolveStuckAuthorizations handles 0 sessions
    Then customer-payments received 1 authorization call
    When 2 seconds pass
    And ResolveStuckAuthorizations handles 1 session
    Then customer-payments received 2 authorization calls
    And the authorization keys were "sess-1:auth:0, sess-1:auth:0"
    And customer-payments received the tokens "tok_1, tok_1"

  Scenario: A stuck authorization whose provider is still down stays AUTHORIZING, is never voided or placed, and settles on recovery
    Given customer-payments is unreachable for authorizations
    And I confirm the session with token "tok_1"
    When 3 minutes pass
    And ResolveStuckAuthorizations handles 1 session
    And I read the session
    Then the response field "state" is AUTHORIZING
    And no void was sent
    And customer-ordering received 0 calls
    When customer-payments recovers
    And ResolveStuckAuthorizations handles 1 session
    And I read the session
    Then the response field "state" is COMPLETED
    And the authorization keys were "sess-1:auth:0, sess-1:auth:0, sess-1:auth:0"
    And the events published are "CheckoutOpened, CheckoutCompleted"

  Scenario: A stuck authorization that turns out declined opens the session to another token as a new attempt
    Given customer-payments is unreachable for authorizations
    And customer-payments declines with "insufficient-funds"
    And I confirm the session with token "tok_1"
    When 3 minutes pass
    And customer-payments recovers
    And ResolveStuckAuthorizations handles 1 session
    And I read the session
    Then the response field "state" is DECLINED
    And the CheckoutDeclined event carries the reason "insufficient-funds"
    When I confirm the session with token "tok_2"
    Then the API answers 200
    And the response field "state" is COMPLETED
    And the authorization keys were "sess-1:auth:0, sess-1:auth:0, sess-1:auth:1"
    And customer-payments received the tokens "tok_1, tok_1, tok_2"

  # --- ExpireOnQuoteExpiry ----------------------------------------------------------

  Scenario: An open session is left alone until its quote expires and expires once
    When 899 seconds pass
    And ExpireOnQuoteExpiry handles 0 sessions
    And I read the session
    Then the response field "state" is OPEN
    When 2 seconds pass
    And ExpireOnQuoteExpiry handles 1 session
    And ExpireOnQuoteExpiry handles 0 sessions
    And I read the session
    Then the response field "state" is EXPIRED
    And the events published are "CheckoutOpened, CheckoutSessionExpired"

  Scenario Outline: Only an OPEN or DECLINED session expires with its quote, never one with a saga in flight or closed
    Given <setup>
    And I confirm the session with token "tok_1"
    When 16 minutes pass
    And ExpireOnQuoteExpiry handles 0 sessions
    And I read the session
    Then the response field "state" is <state>
    And the "CheckoutSessionExpired" event was published 0 times

    Examples:
      | setup                                                                | state            |
      | customer-payments is unreachable for authorizations                  | AUTHORIZING      |
      | customer-ordering times out                                          | PLACING          |
      | customer-ordering answers a definitive 409 requoting offer "offer-9" | REQUOTE_REQUIRED |
      | customer-ordering recovers                                           | COMPLETED        |

  # --- PurgeStoredPlacements ---------------------------------------------------------

  Scenario: The stored placement is kept until 24 hours after the terminal state and the session stays readable
    When I confirm the session with token "tok_1"
    And 23 hours and 59 minutes pass
    And PurgeStoredPlacements handles 0 sessions
    Then the stored placement of the session is kept
    When 2 minutes pass
    And PurgeStoredPlacements handles 1 session
    Then the stored placement of the session is gone
    When I read the session
    Then the API answers 200
    And the response field "state" is COMPLETED
    And the response field "orderId" is order-sess-1

  Scenario: The 24 hours count from the terminal state, not from the confirm
    Given customer-ordering times out
    And I confirm the session with token "tok_1"
    When 5 hours pass
    And customer-ordering recovers
    And ResolveInDoubtPlacements handles 1 session
    And 23 hours and 59 minutes pass
    And PurgeStoredPlacements handles 0 sessions
    Then the stored placement of the session is kept
    When 2 minutes pass
    And PurgeStoredPlacements handles 1 session
    Then the stored placement of the session is gone

  Scenario: A placement that is still in doubt is never purged, however old
    Given customer-ordering times out
    And I confirm the session with token "tok_1"
    When 30 hours pass
    And PurgeStoredPlacements handles 0 sessions
    Then the stored placement of the session is kept
