Feature: Get order

  @AC-126
  Scenario: A customer reads the own order
    Given a customer's order was cancelled after its pickup
    When the customer reads the order
    Then the customer sees its items, total, state, recorded facts and cancellation reason

  @AC-127
  Scenario Outline: A subject without the permission learns nothing about an order
    Given an order is placed
    When another customer without the permission reads <target> order
    Then access is denied

    Examples:
      | target      |
      | an existing |
      | a missing   |

  @AC-127
  Scenario: A subject with the permission learns that an order is missing
    Given an order is placed
    When a support subject with the permission reads a missing order
    Then the order is reported as not found

  @AC-504
  Scenario Outline: An order that may still be cancelled shows its cancellation window
    Given an order is <state>
    When the customer reads the order
    Then the order shows the cancellation window from its placement

    Examples:
      | state     |
      | placed    |
      | confirmed |

  @AC-505
  Scenario Outline: A final order shows no cancellation window
    Given an order is <state>
    When the customer reads the order
    Then the order shows no cancellation window

    Examples:
      | state     |
      | cancelled |
      | completed |

  @AC-507
  Scenario: A failing cancellation policy hides the window
    Given an order is placed and the cancellation policy is failing
    When the customer reads the order
    Then the order is shown
    And the order shows no cancellation window
