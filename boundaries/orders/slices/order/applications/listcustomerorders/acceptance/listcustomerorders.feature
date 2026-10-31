Feature: List customer orders

  @AC-128
  Scenario: A customer reads the own history page by page
    Given a customer has three orders and another customer has one
    When the customer reads the history two orders at a time
    Then the first page holds the two newest orders of the customer
    And the next page holds the oldest order and ends the history
    And every order of the history carries its full card

  @AC-129
  Scenario: Another subject without the permission cannot read the history
    Given a customer has three orders and another customer has one
    When another customer without the permission reads the history of the customer
    Then access is denied

  @AC-129
  Scenario: A subject with the permission reads the history of the customer
    Given a customer has three orders and another customer has one
    When a support subject with the permission reads the history of the customer
    Then the history lists order-c, order-b, order-a

  @pending
  Scenario Outline: The history lists the orders of one market
    Given a customer has three orders and another customer has one
    And the customer has an order in hamburg
    When the customer reads the history in <market>
    Then the history lists <orders>

    Examples:
      | market  | orders                    |
      | berlin  | order-c, order-b, order-a |
      | hamburg | order-h                   |
      | paris   | no orders                 |
