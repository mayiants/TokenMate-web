import type { ReactNode } from 'react';

/** Unified page head for every console page: title (+ optional subtitle).
 *  Only Dashboard carries a subtitle (greeting); other pages title-only. */
export default function PageHead({ title, sub, extra }: { title: ReactNode; sub?: ReactNode; extra?: ReactNode }) {
  return (
    <div className="wg-page-head">
      <div>
        <h2 className="wg-page-title">{title}</h2>
        {sub && <p className="wg-page-sub">{sub}</p>}
      </div>
      {extra}
    </div>
  );
}
