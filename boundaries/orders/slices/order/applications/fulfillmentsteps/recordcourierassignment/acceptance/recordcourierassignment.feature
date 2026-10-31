Feature: Record courier assignment

  @AC-002
  Scenario: Courier assignment lets the order continue
    Given an order is waiting for courier assignment
    When a courier is assigned to the delivery
    Then the order records the courier assignment
    And order fulfillment can continue

  @AC-106
  Scenario: The same assignment is delivered again
    Given an order has recorded a courier assignment
    When the same assignment is delivered again
    Then the order does not change again
    And order fulfillment is told of the assignment again

  @AC-112
  Scenario Outline: An assignment about a final order changes nothing
    Given an order is <state>
    When an assignment arrives after order fulfillment is closed
    Then the order does not change
    And it counts as processed late

    Examples:
      | state     |
      | completed |
      | cancelled |
