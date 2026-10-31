Feature: Record preparation delay

  @AC-771
  Scenario: A delay replaces the expected ready time
    Given the restaurant has accepted an order and named a ready time
    When the restaurant reports a delay with a later ready time
    Then the order expects the later ready time
    And the order stays confirmed
    And order fulfillment is told of the later ready time

  @AC-106
  Scenario: The same delay is delivered again
    Given an order has recorded a delay
    When the same delay is delivered again
    Then the order does not change again
    And order fulfillment is told of the delay again

  @AC-112
  Scenario Outline: A delay of a final order changes nothing
    Given an order is <state>
    When a delay arrives after order fulfillment is closed
    Then the order expects no ready time
    And the delay counts as processed late

    Examples:
      | state     |
      | completed |
      | cancelled |
