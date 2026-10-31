Feature: Plan schedule

  @pending
  Scenario: The plan counts back from the delivery window
    Given a scheduled order with a delivery window at 19:00
    And the restaurant prepares in 25 minutes
    And the market delivers with a 15 minute courier lead and a 20 minute transit
    When order fulfillment plans the schedule
    Then the order is planned ready at 18:40
    And preparation is planned to start at 18:10
    And the courier is planned to be requested at 18:25
    And the plan is checked again at 17:40

  @pending
  Scenario: A replan keeps the ready time the kitchen was given
    Given a scheduled order with a delivery window at 19:00
    And the restaurant prepares in 35 minutes
    And the market delivers with a 15 minute courier lead and a 30 minute transit
    When order fulfillment replans the schedule with the ready time 18:40
    Then the order is planned ready at 18:40
    And preparation is planned to start at 18:00

  @pending
  Scenario: An order without a delivery window has no plan
    Given an order delivered as soon as possible
    When order fulfillment tries to plan the schedule
    Then the plan is refused because the order is not scheduled
