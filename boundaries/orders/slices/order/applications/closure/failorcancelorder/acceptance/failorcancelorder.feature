Feature: Fail or cancel order

  @AC-123
  Scenario Outline: The negative outcome is recorded with its reason
    Given an order is <state>
    When order fulfillment records a <outcome> with reason <reason>
    Then the order is cancelled with that outcome and reason
    And the outcome is published

    Examples:
      | state     | outcome      | reason              |
      | placed    | failure      | restaurant_rejected |
      | confirmed | cancellation | customer_requested  |

  @AC-124
  Scenario: Recording the outcome again changes nothing
    Given an order has already received a negative outcome
    When order fulfillment records the outcome again
    Then the order reports the outcome as already recorded
    And the outcome is published once

  @AC-125
  Scenario: A completed order cannot receive a negative outcome
    Given an order is completed
    When order fulfillment tries to cancel the order
    Then the negative outcome is refused
    And the completed order is not changed
