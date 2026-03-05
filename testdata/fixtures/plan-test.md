# Plan: Add Calculator CLI

## Overview
Add a simple calculator mode to the sample project that can evaluate basic math expressions from command-line arguments.

## Stories

1. Add a `Calculate(op string, a, b int) (int, error)` function to math.go that takes an operator string (+, -, *) and two ints, and returns the result. Return an error for unknown operators.

2. Update main.go to accept command-line arguments: `./sample + 2 3` should print `5`. If no args provided, keep the current "hello world" behavior.
