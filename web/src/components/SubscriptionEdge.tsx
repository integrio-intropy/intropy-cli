import { BaseEdge, EdgeLabelRenderer, getBezierPath, type Edge, type EdgeProps } from '@xyflow/react'
import type { TopicRef } from '../api'
import { AsteriskIcon, FilterIcon } from '../icons'

/** The subscription a topic → subscriber edge draws. */
export interface SubscriptionEdgeData extends Record<string, unknown> {
  subscription: TopicRef
}

export type SubscriptionEdgeType = Edge<SubscriptionEdgeData, 'subscription'>

/** Whether any of the subscription's routes filters on content. */
export function hasConditions(s: TopicRef): boolean {
  return (s.routes ?? []).some((r) => !!r.when)
}

// SubscriptionEdge draws a topic → subscriber edge with a legend chip at its
// midpoint: an asterisk when no route filters on content, a filter glyph when
// one does. Either chip opens the subscription's routing rules on hover or
// focus — every message it routes, each with its condition, and what becomes
// of the rest of the topic.
export function SubscriptionEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  markerEnd,
  data,
}: EdgeProps<SubscriptionEdgeType>) {
  const [path, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  })
  const subscription = data?.subscription
  if (!subscription) return <BaseEdge id={id} path={path} markerEnd={markerEnd} />

  const filtered = hasConditions(subscription)
  const summary = filtered ? 'Content-filtered routes' : 'No content filters'
  return (
    <>
      <BaseEdge id={id} path={path} markerEnd={markerEnd} />
      <EdgeLabelRenderer>
        <div
          className="rf-sub-legend nodrag nopan"
          style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)` }}
        >
          <button
            type="button"
            className={`rf-sub-chip${filtered ? ' filtered' : ''}`}
            aria-label={`${summary}: show the subscription's routing rules`}
          >
            {filtered ? <FilterIcon aria-hidden /> : <AsteriskIcon aria-hidden />}
          </button>
          <RouteRules subscription={subscription} summary={summary} />
        </div>
      </EdgeLabelRenderer>
    </>
  )
}

// RouteRules lists the subscription's routes in the order the sidecar
// evaluates them, then its default for the events none of them matches.
function RouteRules({ subscription, summary }: { subscription: TopicRef; summary: string }) {
  const routes = subscription.routes ?? []
  return (
    <div className="rf-sub-popover" role="tooltip">
      <div className="rf-sub-popover-title">{summary}</div>
      {routes.length === 0 ? (
        <div className="rf-sub-rule-all">Every message on {subscription.topic}</div>
      ) : (
        <ol className="rf-sub-rules">
          {routes.map((r, i) => (
            <li key={`${i}:${r.message}`}>
              <span className="rf-sub-message">{r.message}</span>
              {r.when ? (
                <code className="rf-sub-condition">{r.when}</code>
              ) : (
                <span className="rf-sub-rule-all">every event</span>
              )}
            </li>
          ))}
        </ol>
      )}
      {subscription.default && (
        <div className="rf-sub-unhandled">
          Anything else:{' '}
          {subscription.default === 'ignore' ? 'acknowledged and dropped' : 'left for the broker to dead-letter'}
        </div>
      )}
    </div>
  )
}
