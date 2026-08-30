# Feature Specification: [FEATURE NAME]

**Feature Branch**: `[###-feature-name]`

**Created**: [DATE]

**Status**: Draft

**Input**: User description: "$ARGUMENTS"

## User Scenarios & Testing *(mandatory)*

<!--
  IMPORTANT: User stories should be PRIORITIZED as user journeys ordered by importance.
  Each user story/journey must be INDEPENDENTLY TESTABLE - meaning if you implement just ONE of them,
  you should still have a viable MVP (Minimum Viable Product) that delivers value.

  Assign priorities (P1, P2, P3, etc.) to each story, where P1 is the most critical.
  Think of each story as a standalone slice of functionality that can be:
  - Developed independently
  - Tested independently
  - Deployed independently
  - Demonstrated to users independently
-->

### User Story 1 - [Brief Title] (Priority: P1)

[Describe this user journey in plain language]

**Why this priority**: [Explain the value and why it has this priority level]

**Independent Test**: [Describe how this can be tested independently - e.g., "Can be fully tested by [specific action] and delivers [specific value]"]

**Acceptance Scenarios**:

1. **Given** [initial state], **When** [action], **Then** [expected outcome]
2. **Given** [initial state], **When** [action], **Then** [expected outcome]

---

### User Story 2 - [Brief Title] (Priority: P2)

[Describe this user journey in plain language]

**Why this priority**: [Explain the value and why it has this priority level]

**Independent Test**: [Describe how this can be tested independently]

**Acceptance Scenarios**:

1. **Given** [initial state], **When** [action], **Then** [expected outcome]

---

### User Story 3 - [Brief Title] (Priority: P3)

[Describe this user journey in plain language]

**Why this priority**: [Explain the value and why it has this priority level]

**Independent Test**: [Describe how this can be tested independently]

**Acceptance Scenarios**:

1. **Given** [initial state], **When** [action], **Then** [expected outcome]

---

[Add more user stories as needed, each with an assigned priority]

### Edge Cases

<!--
  ACTION REQUIRED: The content in this section represents placeholders.
  Fill them out with the right edge cases.
-->

- What happens when [boundary condition]?
- How does the tool handle [error scenario], and which exit code does it return
  (`1` runtime failure, `2` usage error)?
- What does the user see when input arrives on stdin from a pipe rather than a TTY?
- What happens when the user interrupts the command mid-run (`SIGINT`)?
- What happens when a destructive action is requested without `--yes`?

## Requirements *(mandatory)*

<!--
  ACTION REQUIRED: The content in this section represents placeholders.
  Fill them out with the right functional requirements.
-->

### Functional Requirements

- **FR-001**: The tool MUST [specific capability, e.g., "read records from the named source"]
- **FR-002**: The tool MUST [specific capability, e.g., "reject a malformed record with a usage error"]
- **FR-003**: Users MUST be able to [key interaction, e.g., "select the output format with `--output`"]
- **FR-004**: The tool MUST write [what data] to stdout and nothing else
- **FR-005**: The tool MUST report [which conditions] on stderr and exit `1`, and [which
  conditions] on stderr and exit `2`

The stdout/stderr split, the exit codes, and the `--output=text|json` selection are
user-observable behaviour and belong here — they are the tool's contract with the pipe
(Constitution Principle II), not implementation detail.

*Example of marking unclear requirements:*

- **FR-006**: System MUST authenticate users via [NEEDS CLARIFICATION: auth method not specified - email/password, SSO, OAuth?]
- **FR-007**: System MUST retain user data for [NEEDS CLARIFICATION: retention period not specified]

### Key Entities *(include if feature involves data)*

- **[Entity 1]**: [What it represents, key attributes without implementation]
- **[Entity 2]**: [What it represents, relationships to other entities]

## Success Criteria *(mandatory)*

<!--
  ACTION REQUIRED: Define measurable success criteria.
  These must be measurable and free of implementation detail. The observable CLI
  contract — what lands on stdout, what lands on stderr, and the exit code — is
  behaviour, not implementation, and is fair game here.
-->

### Measurable Outcomes

- **SC-001**: [Measurable metric, e.g., "Processes a 100k-line input in under 2 seconds"]
- **SC-002**: [Measurable metric, e.g., "`--output=json` output parses with `jq` on every documented input"]
- **SC-003**: [Composability metric, e.g., "Piping the output into [tool] requires no `2>/dev/null`
  because no diagnostic reaches stdout"]
- **SC-004**: [Reliability metric, e.g., "Interrupting mid-run leaves no partial file behind"]

## Assumptions

<!--
  ACTION REQUIRED: The content in this section represents placeholders.
  Fill them out with the right assumptions based on reasonable defaults
  chosen when the feature description did not specify certain details.
-->

- [Assumption about target users, e.g., "Users invoke the tool from a shell script as often as interactively"]
- [Assumption about scope boundaries, e.g., "This feature serves the binary's single job; anything
  beyond it belongs in a separate tool (Constitution Principle I)"]
- [Assumption about data/environment, e.g., "Input arrives on stdin or as a file path argument"]
- [Assumption about dependencies, e.g., "This feature is implementable with the standard library;
  any direct module dependency needs the author's prior approval (Constitution Principle VI)"]
