Feature: Record courier assignment

  @AC-002
  Scenario: Courier assignment lets the order continue
    Given an order is waiting for courier assignment
    When a courier is assigned to the delivery
    Then the order records the courier assignment
    And order fulfillment can continue
