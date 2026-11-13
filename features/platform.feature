# platform.feature — wave 2: the platform endpoints (healthz and readyz were
# the only routes the suite never hit).
Feature: The platform endpoints

  Scenario: Liveness and readiness answer
    When I GET "/healthz"
    Then the API answers 200
    When I GET "/readyz"
    Then the API answers 200
    And the response field "status" is ready

  Scenario: The drain gate flips readiness off
    When the drain gate flips to not-ready
    And I GET "/readyz"
    Then the API answers 503
    And the response field "status" is not_ready
    When I GET "/healthz"
    Then the API answers 200
