// GraphLegend shows the color legend for entity types and community rings
// in the knowledge graph. Entity types determine the dot color; community
// determines the outer ring color (Louvain detection).

import { ENTITY_TYPES, communityColor } from "./entityTypes";
import { useT } from "../../lib/i18n";

export function GraphLegend({ hasCommunities = false }: { hasCommunities?: boolean }) {
  const t = useT();
  return (
    <div className="rag-legend">
      <div className="rag-legend__group">
        <span className="rag-legend__title">{t("graphLegend.type")}</span>
        {ENTITY_TYPES.map((item) => (
          <div key={item.key} className="rag-legend__item">
            <span className="rag-legend__dot" style={{ background: item.color }} />
            <span className="rag-legend__label">{item.label}</span>
          </div>
        ))}
      </div>
      {hasCommunities && (
        <div className="rag-legend__group">
          <span className="rag-legend__title">{t("graphLegend.community")}</span>
          <div className="rag-legend__item">
            <span
              className="rag-legend__ring"
              style={{ borderColor: communityColor(0) }}
            />
            <span className="rag-legend__label">{t("graphLegend.nodeRing")}</span>
          </div>
        </div>
      )}
    </div>
  );
}
