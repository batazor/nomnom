# 1. Вести архитектурные решения control plane

Date: 2026-10-26

## Status

Accepted

## Context

Control plane ([ADR 41](../../../../docs/adr/0041-cell-based-architecture-with-control-plane.md)) —
самостоятельная boundary `boundaries/controlplane`. Ей нужен собственный журнал решений, чтобы
её архитектурная история находилась рядом с кодом и не смешивалась с общесистемными решениями
nomnom.

## Decision

Архитектурные решения, принадлежащие только control plane, документируются в виде ADR в
директории `boundaries/controlplane/docs/adr`. Для управления локальным журналом используется
[adr-tools](https://github.com/npryce/adr-tools).

## Consequences

Решения control plane хранятся рядом с boundary и имеют собственную нумерацию. Общесистемные
решения — Cell, Market, маршрутизация по Market — по-прежнему фиксируются в корневом журнале
`docs/adr`, поэтому при создании ADR нужно выбрать журнал по области действия решения.
