Feature: Confirm order

  @AC-001
  Scenario: Restaurant acceptance lets the order continue
    Given an order is waiting for restaurant confirmation
    When the restaurant accepts the order
    Then the order records the restaurant's acceptance
    And order fulfillment can continue

  @AC-003
  Scenario: Restaurant acceptance is received again
    Given an order has already recorded the restaurant's acceptance
    When the restaurant accepts the order again
    Then the order still records one restaurant acceptance
    And no additional business effect occurs
