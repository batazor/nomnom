Feature: Complete order

  @AC-120
  Scenario: A delivered order is completed
    Given an order is delivered
    When order fulfillment completes the order
    Then the order is completed
    And the completion is published with its moment

  @AC-121
  Scenario: Completing a completed order changes nothing
    Given an order has already been completed
    When order fulfillment completes the order again
    Then the order stays completed with one published completion

  @AC-122
  Scenario Outline: An order that is not delivered cannot be completed
    Given an order is <state>
    When order fulfillment tries to complete the order
    Then the completion is refused
    And the order is not changed

    Examples:
      | state     |
      | placed    |
      | picked_up |
      | cancelled |
