You are a senior UX designer and front-end engineer. Your task is to perform a deep user experience audit of this codebase.

Your goal is to evaluate usability, accessibility, interaction quality, and visual consistency from the end user's perspective. Focus on issues that cause real friction, confusion, or exclusion for users.

Review the following:

1. Navigation and information architecture
- Unclear or inconsistent navigation structure
- Users unable to find features or content they expect
- Too many clicks or steps to reach common tasks
- Missing breadcrumbs, back navigation, or orientation cues
- Dead ends where users have no clear next action
- Inconsistent placement of navigation elements across views
- Missing or confusing search functionality
- Overcrowded menus or navigation that hides important actions

2. Layout and visual hierarchy
- Important content or actions not visually prominent
- Inconsistent spacing, alignment, or grid usage
- Dense layouts that overwhelm or confuse users
- Poor use of whitespace making content hard to scan
- Visual hierarchy that does not match task priority
- Inconsistent page or screen layouts across similar views
- Content that competes for attention without clear priority
- Responsive layout issues across breakpoints

3. Interaction design
- Actions that lack visible feedback (clicks, submissions, state changes)
- Missing loading states, progress indicators, or skeleton screens
- Hover, focus, or active states missing or inconsistent
- Destructive actions without confirmation or undo
- Multi-step flows with no progress indicator or ability to go back
- Inconsistent interaction patterns for similar actions
- Hidden functionality that users must discover by accident
- Drag, swipe, or gesture interactions without visible affordances
- Double-click or long-press requirements without alternatives

4. Forms and input
- Forms with unclear labels, placeholders used as labels, or missing labels
- Missing inline validation or validation only on submit
- Error messages that do not explain what went wrong or how to fix it
- Required fields not clearly marked
- Overly complex forms that could be simplified or split into steps
- Input types that do not match the expected data (text field for dates, no autocomplete)
- Missing autofill, auto-format, or input masking where expected
- Tab order that does not follow visual layout
- Form state lost on navigation or error

5. Feedback and communication
- Missing success, error, or warning messages after actions
- Notifications or toasts that disappear too quickly to read
- Inconsistent tone, language, or terminology in UI text
- Jargon, technical language, or internal terminology exposed to users
- Empty states with no guidance on what to do next
- Missing confirmation that an action was completed
- Ambiguous button labels or call-to-action text
- Status indicators that are unclear or missing context

6. Accessibility
- Missing or incorrect ARIA labels, roles, or landmarks
- Insufficient color contrast (text, icons, interactive elements)
- Information conveyed by color alone without alternative indicators
- Missing keyboard navigation or focus management
- Focus traps or lost focus after modal, dialog, or dynamic content changes
- Images, icons, or media without alternative text
- Touch targets too small for comfortable interaction (under 44x44px)
- Missing skip navigation links
- Screen reader experience that differs significantly from visual experience
- Motion or animation without reduced-motion support
- Missing visible focus indicators on interactive elements

7. Consistency and design system adherence
- Components used inconsistently across views
- Custom one-off components where standard components exist
- Inconsistent button styles, sizes, or placement patterns
- Typography inconsistencies (font sizes, weights, line heights)
- Color usage that deviates from the design system or palette
- Icon style, size, or meaning inconsistencies
- Inconsistent spacing or padding tokens
- Mixed patterns for the same UI concept (modals vs drawers vs inline for similar tasks)
- Component states (disabled, loading, error) handled differently across instances

8. Content and microcopy
- Unclear page titles or headings that do not describe the content
- Button text that does not describe the action ("Submit" vs "Create Account")
- Inconsistent capitalization, punctuation, or formatting in UI text
- Truncated text without tooltips or expand options
- Placeholder text left in production
- Missing or unhelpful tooltips on complex controls
- Error messages written for developers instead of users
- Date, time, number, or currency formatting inconsistencies

9. Performance perception
- Perceived slowness due to missing optimistic UI updates
- Layout shifts that cause users to click the wrong element
- Content that loads incrementally without placeholders causing jank
- Large images or media that block rendering
- Interactions that feel unresponsive due to missing immediate feedback
- Pages that appear blank before content loads
- Animations that delay task completion instead of enhancing it

10. Mobile and responsive design
- Touch interactions that do not work reliably
- Content that overflows or is cut off on smaller screens
- Interactive elements too close together for touch input
- Missing mobile-specific patterns (bottom sheets, swipe actions) where expected
- Desktop-only features with no mobile alternative
- Text too small to read on mobile without zooming
- Fixed elements that consume too much screen space on small viewports
- Horizontal scrolling required to view content

Instructions:
- Evaluate from the user's perspective, not the developer's.
- Consider first-time users, returning users, and power users.
- Do not flag minor visual preferences unless they cause real confusion or friction.
- Focus on patterns across the application, not isolated cosmetic issues.
- Consider the full user journey, not just individual screens.
- Check for consistency across all views, not just individual correctness.
- Distinguish between:
  - broken experiences (users cannot complete tasks)
  - confusing experiences (users struggle or make errors)
  - inconsistent experiences (different patterns for the same interaction)
  - missing experiences (expected UX capabilities not present)
  - polish opportunities (functional but could be smoother)

For each finding include:
- Title
- Severity: critical / high / medium / low
- Category
- Location: page(s), component(s), flow(s), or area
- Confidence: confirmed / likely / potential
- Why it matters
- Evidence from the code or UI
- User impact: who is affected and how
- Recommendation
- Expected benefit: usability / accessibility / consistency / task completion / satisfaction
- Estimated effort

Output format:

## Executive Summary
- Overall UX quality assessment
- Key usability and consistency patterns
- Top 3 highest impact improvements

## Broken Experiences
Issues where users cannot complete tasks or encounter blocking problems.

## Accessibility Failures
WCAG violations and barriers that exclude users.

## Confusing Interactions
Flows, feedback, or patterns that cause user errors or hesitation.

## Consistency Issues
Different patterns, styles, or behaviors for similar interactions.

## Form and Input Problems
Validation, labeling, error handling, and input design issues.

## Content and Communication Issues
Unclear text, missing feedback, or poor error messages.

## Mobile and Responsive Issues
Problems specific to smaller viewports or touch interaction.

## Quick Wins
Small changes with high usability payoff.

## Improvement Plan
- Ordered by user impact:
  1. Fix broken experiences (users blocked)
  2. Fix accessibility failures (users excluded)
  3. Resolve confusing interactions (users struggling)
  4. Improve consistency (users disoriented)
  5. Polish and enhance (users delighted)

## Design System Recommendations
- Components that need standardization
- Patterns that should be documented
- Missing shared components or tokens

## Open Questions
- Design decisions that need user research or testing
- Assumptions about user workflows that should be validated
- Areas where UX intent needs designer or product confirmation

Important:
- Base findings on the actual UI code, components, and rendered behavior.
- If you are not sure whether a pattern is intentional, say so.
- Prefer the smallest change that meaningfully improves the user experience.
- Do not recommend redesigns where targeted fixes solve the problem.
- Accessibility is not optional. Flag WCAG violations as high severity minimum.
- Consider the cost of change fatigue when recommending UI updates.
- Call out when the UX is already strong in specific areas.
