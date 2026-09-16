import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { MinimalCullSection, MinimalThemeProvider } from "@ovasabi/ui-minimal";

describe("MinimalCullSection (dom)", () => {
  it("renders the requested element with its estimate as a variable and forwards props", () => {
    render(
      <MinimalThemeProvider>
        <MinimalCullSection as="section" estimatedSize="320px" style={{ color: "red" }} aria-label="Week 3">
          Rows
        </MinimalCullSection>
      </MinimalThemeProvider>,
    );
    const section = screen.getByLabelText("Week 3");
    expect(section.tagName).toBe("SECTION");
    expect(section.getAttribute("data-minimal")).toBe("CullSection");
    expect(section.style.getPropertyValue("--minimal-cull-estimate")).toBe("320px");
    expect(section.style.color).toBe("red");
    expect(section.textContent).toBe("Rows");
  });

  it("defaults the estimate and lets a caller's style win over nothing else", () => {
    render(
      <MinimalThemeProvider>
        <MinimalCullSection data-testid="cull">Rows</MinimalCullSection>
      </MinimalThemeProvider>,
    );
    const section = screen.getByTestId("cull");
    expect(section.tagName).toBe("DIV");
    expect(section.style.getPropertyValue("--minimal-cull-estimate")).toBe("600px");
  });

  it("keeps culled content in the document, where assistive tech and search can reach it", () => {
    render(
      <MinimalThemeProvider>
        {Array.from({ length: 30 }, (_, index) => (
          <MinimalCullSection key={index} estimatedSize="200px">
            <p>Section {index}</p>
          </MinimalCullSection>
        ))}
      </MinimalThemeProvider>,
    );
    expect(screen.getByText("Section 29")).toBeTruthy();
  });
});
