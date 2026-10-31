Feature: Start fulfillment

  @AC-827
  Scenario: A placed order starts its fulfillment
    Given an order is placed
    When the placement of the order is received
    Then order fulfillment is started once
    And the placement counts as processed

  @AC-819
  Scenario: A repeated placement starts fulfillment again
    Given the placement of an order has been received
    When the same placement is received again
    Then order fulfillment is started again
    And the repeated placement counts as a duplicate

  @AC-820
  Scenario Outline: The placement of a final order starts nothing
    Given an order is <state>
    When the placement of the final order is received twice
    Then order fulfillment is not started
    And the placement counts as late both times

    Examples:
      | state     |
      | completed |
      | cancelled |

  @AC-828
  Scenario: The placement of an unknown order starts nothing
    Given an order is placed
    When the placement of an unknown order is received
    Then the order is reported as not found
    And order fulfillment is not started

  @pending @FR-415
  Scenario: A scheduled order starts fulfillment with its delivery window
    Given a scheduled order is placed
    When the placement of the scheduled order is received
    Then order fulfillment is started with the delivery window
