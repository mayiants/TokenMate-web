/**
 * Client-side table pagination for admin pages.
 * Backend returns all rows; frontend paginates via CSS display toggling.
 * Page size is persisted per pathname in localStorage.
 */

(function() {
  'use strict';

  window.initTablePagination = function(selector, opts) {
    opts = opts || {};
    var table = document.querySelector(selector);
    if (!table) return;
    var tbody = table.querySelector('tbody');
    if (!tbody) return;
    var allRows = Array.from(tbody.querySelectorAll('tr'));
    if (allRows.length <= 10) return; // too few rows to paginate

    var pageSizeKey = 'admin_page_size_' + location.pathname;
    var pageSize = parseInt(localStorage.getItem(pageSizeKey)) || opts.defaultPageSize || 20;
    if (![10, 20, 50].includes(pageSize)) pageSize = 20;

    var currentPage = 1;
    var totalPages = Math.max(1, Math.ceil(allRows.length / pageSize));

    // Find insertion point: after the table wrapper or the table itself
    var wrapper = table.closest('.data-table-wrapper') || table.parentNode;
    var card = wrapper.closest('.card');
    var insertAfter = wrapper;

    // Create controls container
    var controls = document.createElement('div');
    controls.className = 'pagination-controls flex items-center justify-between mt-4 px-2';
    controls.innerHTML =
      '<div class="flex items-center gap-2">' +
        '<span class="text-sm text-muted">' + _t('Per page') + '</span>' +
        '<select class="form-select form-select-sm page-size-select" aria-label="' + _t('Rows per page') + '" style="width:70px">' +
          '<option value="10"' + (pageSize === 10 ? ' selected' : '') + '>10</option>' +
          '<option value="20"' + (pageSize === 20 ? ' selected' : '') + '>20</option>' +
          '<option value="50"' + (pageSize === 50 ? ' selected' : '') + '>50</option>' +
        '</select>' +
        '<span class="text-sm text-muted">' + _t('items') + '</span>' +
      '</div>' +
      // Prev / info / Next in that order — same arrangement as the shared
      // renderPagination() and the server-rendered pagination blocks.
      '<div class="flex items-center gap-2">' +
        '<button class="btn btn-sm btn-secondary prev-btn">' + _t('Previous') + '</button>' +
        '<span class="text-sm text-muted pagination-info"></span>' +
        '<button class="btn btn-sm btn-secondary next-btn">' + _t('Next') + '</button>' +
      '</div>';

    insertAfter.parentNode.insertBefore(controls, insertAfter.nextSibling);

    var sizeSelect = controls.querySelector('.page-size-select');
    var prevBtn = controls.querySelector('.prev-btn');
    var nextBtn = controls.querySelector('.next-btn');
    var infoEl = controls.querySelector('.pagination-info');

    function render() {
      var start = (currentPage - 1) * pageSize;
      var end = start + pageSize;
      allRows.forEach(function(row, idx) {
        row.style.display = (idx >= start && idx < end) ? '' : 'none';
      });
      totalPages = Math.max(1, Math.ceil(allRows.length / pageSize));
      if (currentPage > totalPages) currentPage = totalPages;
      prevBtn.disabled = currentPage <= 1;
      nextBtn.disabled = currentPage >= totalPages;
      infoEl.textContent = _t('Total ') + allRows.length + _t(' items, Page ') + currentPage + ' / ' + totalPages;
    }

    sizeSelect.addEventListener('change', function() {
      pageSize = parseInt(this.value);
      localStorage.setItem(pageSizeKey, pageSize);
      currentPage = 1;
      render();
    });

    prevBtn.addEventListener('click', function() {
      if (currentPage > 1) { currentPage--; render(); }
    });

    nextBtn.addEventListener('click', function() {
      if (currentPage < totalPages) { currentPage++; render(); }
    });

    render();

    // Expose a refresh method so callers can re-paginate after filtering
    table._paginationRefresh = function() {
      allRows = Array.from(tbody.querySelectorAll('tr'));
      currentPage = 1;
      render();
    };
  };
})();
