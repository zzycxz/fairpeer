import { useEffect, useRef, useState } from "react";
import mermaid from "mermaid";
import { extractMermaidTitle, sanitizeMermaidCode } from "./mermaidLogic";

// Initialize mermaid with specific settings for the app's aesthetic
mermaid.initialize({
  startOnLoad: false,
  theme: 'dark', // Using the graphite dark theme
  securityLevel: 'strict',
  fontFamily: 'inherit'
});

export function MermaidViewer({ chart }: { chart: string }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [svg, setSvg] = useState<string>("");
  const [error, setError] = useState<string>("");
  
  const title = extractMermaidTitle(chart);
  const safeChart = sanitizeMermaidCode(chart);

  useEffect(() => {
    let cancelled = false;
    
    const renderChart = async () => {
      try {
        // mermaid.render needs a unique id
        const id = `mermaid-${Math.random().toString(36).substr(2, 9)}`;
        const { svg: renderedSvg } = await mermaid.render(id, safeChart);
        
        if (!cancelled) {
          setSvg(renderedSvg);
          setError("");
        }
      } catch (err) {
        if (!cancelled) {
          setError((err as Error).message || "Failed to render Mermaid chart");
        }
      }
    };
    
    if (safeChart) {
      renderChart();
    }
    
    return () => {
      cancelled = true;
    };
  }, [safeChart]);

  return (
    <div className="mermaid-viewer">
      {title && <div className="mermaid-viewer__title">{title}</div>}
      
      {error ? (
        <div className="mermaid-viewer__error">
          <strong>Mermaid Error:</strong> {error}
          <pre>{safeChart}</pre>
        </div>
      ) : (
        <div 
          ref={containerRef}
          className="mermaid-viewer__svg-container"
          dangerouslySetInnerHTML={{ __html: svg }} 
        />
      )}
    </div>
  );
}
